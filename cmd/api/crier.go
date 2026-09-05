package main

import (
	"context"
	"expvar"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	core "github.com/JonasBorgesLM/crier/core"
	otlp "github.com/JonasBorgesLM/crier/exporters/otlp"

	"github.com/JonasBorgesLM/task-api/internal/config"
)

// crierServiceName identifies this process to crier's backend, the way
// cfg.CrierOTLPEndpoint's collector will see it. A constant rather than
// something derived from config: there is exactly one service here, and
// nothing about its identity is meant to vary between deployments.
const crierServiceName = "task-api"

// crierShutdownTimeout bounds how long crier.Shutdown is allowed to drain
// its buffer during process shutdown. It is provisional — see
// docs/DECISIONS.md's Fase 11 section — set independently of
// cfg.ShutdownTimeout rather than reusing it, because closeAll (where this
// runs) fires *after* srv.Shutdown has already spent up to
// cfg.ShutdownTimeout; stacking a second full budget on top of that would
// silently grow total shutdown time past what
// terminationGracePeriodSeconds in the Kubernetes manifest accounts for.
// Issue 11.7 revisits this once there is a real deployment to load-test
// shutdown against.
const crierShutdownTimeout = 5 * time.Second

// crierHealthCheckMinSeverity raises the effective threshold for
// internal/middleware/logging.go's "http request" line on /health and
// /health/ready to Error, via the AttributeRule below. Liveness/readiness
// probes hit these routes on a short interval and normally return 200 —
// exactly the health-check noise ADR-0022 (crier's attribute-matched
// sampling) exists to narrow for a single-source app like this one.
//
// A real failure still gets through: this codebase's Logging middleware
// only raises the log level to Error for a 5xx response (see its
// doc comment), so a genuinely unready backend is never silenced by this
// rule — only the routine 200s are. It narrows only crier's mirrored
// copy; the primary stdout log is untouched (Filter runs inside crier's
// own pipeline, never the wrapped slog.Handler).
var crierHealthCheckMinSeverity = core.SeverityError

// currentCrierMetrics is what the /debug/vars entries published by
// publishCrierExpvarOnce read from. A package-level atomic pointer rather
// than a value closed over by buildCrier's own caller: expvar.Publish
// panics if called twice with the same name, and newServer (hence
// buildCrier) runs once per *testing.T in this package's own test suite,
// not just once per process the way it does in main(). Publishing the
// expvar.Func closures exactly once and re-pointing what they read on
// every subsequent buildCrier call is what keeps both true at once — the
// closure closes over the package variable, never over a value captured
// at Publish time, so every read reflects whichever crier (if any) is
// current. See docs/DECISIONS.md's Fase 11 section.
var (
	crierExpvarOnce      sync.Once
	currentCrierMetrics  atomic.Pointer[core.CountingMetrics]
	currentCrierInstance atomic.Pointer[core.Crier]
)

// crierDropReasons is every core.DropReason the pipeline can report,
// listed explicitly rather than discovered at runtime — core.Metrics'
// own doc comment calls the set closed by design (a new discard path
// means a new named reason, deliberately, not a string appearing from
// nowhere), so enumerating it here cannot silently fall behind a reason
// nobody added to this list.
var crierDropReasons = []core.DropReason{
	core.DropInvalid,
	core.DropRedactionFailed,
	core.DropSourceQuota,
	core.DropBufferFull,
	core.DropOldest,
	core.DropBackendUnavailable,
	core.DropShutdownTimeout,
}

// publishCrierExpvarOnce registers the crier entries /debug/vars reports —
// buffer depth and records dropped per reason — the first time it is
// called, and does nothing on every call after. It is safe to call
// whether or not crier ends up configured: with nothing ever stored in
// currentCrierInstance/currentCrierMetrics, the published values just
// report a disabled crier (depth 0, no drops), which is the correct
// answer.
//
// Deliberately not wired into GET /health/ready: see 11.8's acceptance
// criteria and docs/DECISIONS.md — a log backend being unreachable is not
// a reason to stop sending this instance traffic.
func publishCrierExpvarOnce() {
	crierExpvarOnce.Do(func() {
		expvar.Publish("crier_buffer_depth", expvar.Func(func() any {
			c := currentCrierInstance.Load()
			if c == nil {
				return 0
			}
			return c.Depth()
		}))
		expvar.Publish("crier_records_dropped", expvar.Func(func() any {
			m := currentCrierMetrics.Load()
			out := make(map[string]int64, len(crierDropReasons))
			for _, reason := range crierDropReasons {
				var n int64
				if m != nil {
					n = m.Snapshot().DroppedBy(reason)
				}
				out[string(reason)] = n
			}
			return out
		}))
	})
}

// buildCrier constructs the embedded crier engine that mirrors every log
// record to cfg.CrierOTLPEndpoint's OTLP/HTTP collector, alongside (never
// instead of) the stdout JSON log.
//
// It returns (nil, nil) when cfg.CrierOTLPEndpoint is empty — crier is
// opt-in, exactly like the attachment backends, and nothing about logging
// changes when it is off.
//
// A non-nil error here means cfg.CrierOTLPEndpoint itself is malformed
// (bad scheme, no host) — a startup-config problem, same tier as a bad
// DATABASE_URL. It is never returned because the collector is
// unreachable: neither otlp.New nor core.New performs any network I/O:
// export happens later, on the dispatcher's own goroutines, so a log
// backend being down is never a reason for the API not to start.
func buildCrier(cfg config.Config) (*core.Crier, error) {
	publishCrierExpvarOnce()

	if cfg.CrierOTLPEndpoint == "" {
		currentCrierInstance.Store(nil)
		currentCrierMetrics.Store(nil)
		return nil, nil
	}

	exporter, err := otlp.New(otlp.Config{Endpoint: cfg.CrierOTLPEndpoint})
	if err != nil {
		return nil, fmt.Errorf("crier: build otlp exporter: %w", err)
	}

	metrics := &core.CountingMetrics{}
	c, err := core.New(core.Options{
		ServiceName:    crierServiceName,
		ServiceVersion: version,
		Exporters:      map[string]core.Exporter{"otlp": exporter},
		Metrics:        metrics,
		// Zero-value guard: caps any attribute key past 1000 distinct
		// values in a 10-minute window. internal/middleware/logging.go
		// logs the raw URL path, and every task ID or file storage key
		// ever created makes "path" an unbounded-cardinality attribute
		// without this (issue #205).
		Cardinality: &core.CardinalityGuard{},
		Filter: &core.Filter{
			Rules: []core.AttributeRule{
				{
					Key:         "path",
					ValuePrefix: "/health",
					MinSeverity: &crierHealthCheckMinSeverity,
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("crier: %w", err)
	}

	currentCrierInstance.Store(c)
	currentCrierMetrics.Store(metrics)
	return c, nil
}

// logDrainSummary logs summary — WARN when records were lost, INFO
// otherwise. Split out from shutdownCrier so the branching can be tested
// directly against a DrainSummary built by hand, rather than needing to
// race crier's real dispatcher to produce a non-empty Lost count on
// demand.
func logDrainSummary(logger *slog.Logger, summary core.DrainSummary) {
	if summary.Clean() {
		logger.Info("crier drain completed", "summary", summary.String())
	} else {
		logger.Warn("crier drain incomplete", "summary", summary.String(), "lost", summary.Lost)
	}
}

// shutdownCrier drains c within ctx and logs the result via
// logDrainSummary. The DrainSummary is never silently discarded: it is
// the only evidence of loss at shutdown (see core.DrainSummary's doc
// comment), and logging it through logger means it reaches stdout
// unconditionally and crier itself when the drain left it enough time to
// accept one more record.
//
// ctx's deadline is the caller's to set — see crierShutdownTimeout's doc
// comment for why closeAll builds it from that constant rather than this
// function hardcoding it.
//
// A nil c is a no-op, so callers do not need to guard the call themselves.
func shutdownCrier(ctx context.Context, c *core.Crier, logger *slog.Logger) error {
	if c == nil {
		return nil
	}

	summary, err := c.Shutdown(ctx)
	logDrainSummary(logger, summary)
	if err != nil {
		return fmt.Errorf("crier: shutdown: %w", err)
	}
	return nil
}

// crierSeverity maps an slog.Level to crier's OTel-aligned Severity. slog
// defines exactly four named levels twelve numeric units apart (Debug -4,
// Info 0, Warn 4, Error 8); a custom level between two named ones takes on
// the lower one's severity, matching slog's own convention that e.g.
// level 6 logs as "INFO+2", not as a level of its own.
func crierSeverity(level slog.Level) core.Severity {
	switch {
	case level >= slog.LevelError:
		return core.SeverityError
	case level >= slog.LevelWarn:
		return core.SeverityWarn
	case level >= slog.LevelInfo:
		return core.SeverityInfo
	default:
		return core.SeverityDebug
	}
}

// crierTeeHandler wraps an slog.Handler and mirrors every record it
// processes to an embedded crier.Crier, in addition to — never instead of
// — passing it through to the wrapped handler exactly as it would without
// crier configured.
//
// This is the one seam where crier reaches this codebase's existing
// logging. It requires no change to any of the ~20 existing
// logger.Error/Info/Warn call sites across cmd/api and every domain
// package: whatever attributes they already pass (request_id, method,
// path, error, ...) are what a record carries to crier too, automatically
// — including request_id, which is how a record is correlated with the
// same request's stdout line downstream.
type crierTeeHandler struct {
	wrapped slog.Handler
	crier   *core.Crier
	attrs   []slog.Attr
}

// newCrierTeeHandler returns a Handler that behaves exactly like wrapped,
// plus the mirroring above. c must not be nil — callers only construct
// this when crier is actually configured (see run()).
func newCrierTeeHandler(wrapped slog.Handler, c *core.Crier) *crierTeeHandler {
	return &crierTeeHandler{wrapped: wrapped, crier: c}
}

func (h *crierTeeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.wrapped.Enabled(ctx, level)
}

func (h *crierTeeHandler) Handle(ctx context.Context, record slog.Record) error {
	// The wrapped handler runs first and its result is what Handle
	// returns: crier is a mirror, never a gate. A record the pipeline
	// rejects (oversized, filtered — see Crier.Log's doc comment) must
	// never affect the primary logging path, which has already
	// succeeded by the time crier sees anything.
	werr := h.wrapped.Handle(ctx, record)

	attrs := make(map[string]any, len(h.attrs)+record.NumAttrs())
	for _, a := range h.attrs {
		attrs[a.Key] = crierAttrValue(a.Value)
	}
	record.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = crierAttrValue(a.Value)
		return true
	})

	_ = h.crier.Log(ctx, core.LogRecord{
		Timestamp:    record.Time,
		Severity:     crierSeverity(record.Level),
		SeverityText: record.Level.String(),
		Body:         record.Message,
		Attributes:   attrs,
	})

	return werr
}

func (h *crierTeeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	merged = append(merged, h.attrs...)
	merged = append(merged, attrs...)
	return &crierTeeHandler{wrapped: h.wrapped.WithAttrs(attrs), crier: h.crier, attrs: merged}
}

func (h *crierTeeHandler) WithGroup(name string) slog.Handler {
	// Groups nest attribute keys for the wrapped handler's own output;
	// core.LogRecord.Attributes is flat, so mirroring a grouped attribute
	// faithfully would need a flattening scheme (dotted keys, most
	// likely). Nothing in this codebase calls logger.WithGroup — every
	// call site passes its attributes inline — so this is left
	// unimplemented for crier's side rather than guessing at a scheme
	// nothing exercises. The wrapped handler's own grouping still works;
	// only the mirrored copy would miss the nesting, and there is none
	// to miss yet.
	return &crierTeeHandler{wrapped: h.wrapped.WithGroup(name), crier: h.crier, attrs: h.attrs}
}

// crierAttrValue converts an slog.Value to the Go type core.LogRecord's
// pipeline actually preserves.
//
// This is not a formatting preference — verified by experiment, not by
// reading crier's source: its Limits stage keeps exactly nil, bool, every
// sized int/uint, float32/64, time.Duration and time.Time, plus string
// and []byte (bounded, not rejected). Anything else — concretely,
// whatever slog.Value wraps as KindAny, which is what "error", err
// becomes, since error is not one of slog's own kinds — is *silently
// replaced* with an "unsupported value type" marker before export,
// discarding it. For an "unexpected service error" log line, that marker
// would be exactly the field a person opens the log to read. Resolve
// (for a fmt.Stringer/error passed via slog.Any) plus explicit
// stringification for anything outside crier's safelist is what keeps
// that field intact.
func crierAttrValue(v slog.Value) any {
	v = v.Resolve()
	switch v.Kind() {
	case slog.KindString, slog.KindBool, slog.KindInt64, slog.KindUint64,
		slog.KindFloat64, slog.KindDuration, slog.KindTime:
		return v.Any()
	default:
		return v.String()
	}
}
