package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"expvar"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/JonasBorgesLM/crier/core"

	commonpb "go.opentelemetry.io/proto/slim/otlp/common/v1"

	collogs "go.opentelemetry.io/proto/slim/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"

	"github.com/JonasBorgesLM/task-api/internal/config"
)

// --- buildCrier ---

func TestBuildCrier_EmptyEndpoint_Disabled(t *testing.T) {
	c, err := buildCrier(config.Config{})
	if err != nil {
		t.Fatalf("buildCrier() with no CrierOTLPEndpoint: unexpected error: %v", err)
	}
	if c != nil {
		t.Error("buildCrier() with no CrierOTLPEndpoint: expected a nil *core.Crier, crier must be opt-in")
	}
}

func TestBuildCrier_MalformedEndpoint_ReturnsError(t *testing.T) {
	// No scheme: otlp.New's own validation rejects this before any
	// network I/O — a startup-config error, same tier as a bad
	// DATABASE_URL, not "the collector is unreachable".
	c, err := buildCrier(config.Config{CrierOTLPEndpoint: "collector.example.com:4318"})
	if err == nil {
		t.Fatal("buildCrier() with a schemeless CrierOTLPEndpoint: expected an error, got nil")
	}
	if c != nil {
		t.Error("buildCrier() on error: expected a nil *core.Crier")
	}
}

// TestBuildCrier_ValidEndpoint_ExportsOverRealHTTP proves the wiring works
// end to end — real HTTP, real gzip, real OTLP/protobuf encoding — rather
// than trusting that composing otlp.New and core.New correctly is enough
// on its own.
func TestBuildCrier_ValidEndpoint_ExportsOverRealHTTP(t *testing.T) {
	type received struct {
		path            string
		contentType     string
		contentEncoding string
		req             *collogs.ExportLogsServiceRequest
	}
	receivedCh := make(chan received, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := io.Reader(r.Body)
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Errorf("collector: gzip.NewReader: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer gz.Close()
			body = gz
		}

		payload, err := io.ReadAll(body)
		if err != nil {
			t.Errorf("collector: read body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		req := &collogs.ExportLogsServiceRequest{}
		if err := proto.Unmarshal(payload, req); err != nil {
			t.Errorf("collector: unmarshal ExportLogsServiceRequest: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		receivedCh <- received{
			path:            r.URL.Path,
			contentType:     r.Header.Get("Content-Type"),
			contentEncoding: r.Header.Get("Content-Encoding"),
			req:             req,
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := buildCrier(config.Config{CrierOTLPEndpoint: srv.URL})
	if err != nil {
		t.Fatalf("buildCrier() unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("buildCrier() with a valid endpoint: expected a non-nil *core.Crier")
	}

	if err := c.Log(context.Background(), core.LogRecord{
		Body:       "hello from TestBuildCrier_ValidEndpoint_ExportsOverRealHTTP",
		Attributes: map[string]any{"request_id": "test-request-id-123"},
	}); err != nil {
		t.Fatalf("Log() unexpected error: %v", err)
	}

	// Shutdown forces the buffered record to flush now rather than
	// waiting out the default batch window, and independently proves the
	// record was not lost (summary.Clean()) — the same drain path
	// production shutdown takes.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	summary, err := c.Shutdown(shutdownCtx)
	if err != nil {
		t.Fatalf("Shutdown() unexpected error: %v", err)
	}
	if !summary.Clean() {
		t.Fatalf("Shutdown() summary = %v, want a clean drain", summary)
	}

	select {
	case got := <-receivedCh:
		if got.path != "/v1/logs" {
			t.Errorf("collector received path %q, want %q", got.path, "/v1/logs")
		}
		if got.contentType != "application/x-protobuf" {
			t.Errorf("Content-Type = %q, want application/x-protobuf", got.contentType)
		}
		if got.contentEncoding != "gzip" {
			t.Errorf("Content-Encoding = %q, want gzip (crier's default compression)", got.contentEncoding)
		}

		body := extractFirstLogBody(t, got.req)
		if body != "hello from TestBuildCrier_ValidEndpoint_ExportsOverRealHTTP" {
			t.Errorf("exported record body = %q, want the logged message", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("collector never received a request within 5s of a clean Shutdown")
	}
}

// extractFirstLogBody digs the body string out of the first log record in
// req's nested resource/scope structure, failing the test if the shape
// does not match what otlp.New's transform is documented to produce.
func extractFirstLogBody(t *testing.T, req *collogs.ExportLogsServiceRequest) string {
	t.Helper()
	if len(req.ResourceLogs) == 0 || len(req.ResourceLogs[0].ScopeLogs) == 0 || len(req.ResourceLogs[0].ScopeLogs[0].LogRecords) == 0 {
		t.Fatalf("ExportLogsServiceRequest has no log records: %+v", req)
	}
	return req.ResourceLogs[0].ScopeLogs[0].LogRecords[0].GetBody().GetStringValue()
}

// allLogRecordAttrs flattens every attribute of every log record across
// req's nested resource/scope structure into one map, keyed by attribute
// name, last write wins. Good enough for a test asserting one record's
// attributes, not a general-purpose decoder.
func allLogRecordAttrs(req *collogs.ExportLogsServiceRequest) map[string]string {
	out := map[string]string{}
	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			for _, rec := range sl.GetLogRecords() {
				for _, kv := range rec.GetAttributes() {
					out[kv.GetKey()] = renderAnyValue(kv.GetValue())
				}
			}
		}
	}
	return out
}

// renderAnyValue renders v as a display string regardless of which OTLP
// scalar type it holds. middleware.Logging passes "status" as an int, not
// a string, so a test reading it back via GetStringValue() alone would
// silently see "" — this exists so allLogRecordAttrs does not repeat that
// mistake.
func renderAnyValue(v *commonpb.AnyValue) string {
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_IntValue:
		return fmt.Sprint(x.IntValue)
	case *commonpb.AnyValue_BoolValue:
		return fmt.Sprint(x.BoolValue)
	case *commonpb.AnyValue_DoubleValue:
		return fmt.Sprint(x.DoubleValue)
	default:
		return ""
	}
}

// decodeOTLPLogsRequest reads and decompresses r's body (gzip, unless
// Content-Encoding says otherwise) and unmarshals it as an OTLP
// ExportLogsServiceRequest, failing the test on any error along the way.
// Shared by every fake collector in this file that needs to actually read
// what the exporter sent, not just acknowledge it.
func decodeOTLPLogsRequest(t *testing.T, r *http.Request) *collogs.ExportLogsServiceRequest {
	t.Helper()

	body := io.Reader(r.Body)
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Fatalf("collector: gzip.NewReader: %v", err)
		}
		defer gz.Close()
		body = gz
	}

	payload, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("collector: read body: %v", err)
	}

	req := &collogs.ExportLogsServiceRequest{}
	if err := proto.Unmarshal(payload, req); err != nil {
		t.Fatalf("collector: unmarshal ExportLogsServiceRequest: %v", err)
	}
	return req
}

// TestRun_CrierMirrorsAccessLogWithRequestID drives the whole composition
// root — run(), not buildCrier or crierTeeHandler in isolation — the same
// way TestRun_GracefulShutdownOnContextCancel does, with CRIER_OTLP_ENDPOINT
// pointed at a fake collector. It is the one test in this file that would
// catch a wiring mistake in run()/newServer themselves: buildCrier never
// called, the tee handler never installed, or crier.Shutdown never reached
// from closeAll — none of which any of the lower-level tests above can see,
// since they construct a crierTeeHandler or a *core.Crier directly.
func TestRun_CrierMirrorsAccessLogWithRequestID(t *testing.T) {
	receivedCh := make(chan *collogs.ExportLogsServiceRequest, 4)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCh <- decodeOTLPLogsRequest(t, r)
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	addr := freeAddr(t)
	t.Setenv("HTTP_ADDR", addr)
	t.Setenv("CRIER_OTLP_ENDPOINT", collector.URL)
	t.Setenv("CSRF_SECRET", testCSRFSecretEnv)

	out := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)
	go func() { runErr <- run(ctx, out) }()

	waitForLogLine(t, out, "server started")

	resp, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	resp.Body.Close()

	// Shut down *before* looking at receivedCh, deliberately: crier's
	// default BatchWindow is 5s (production never overrides it — see
	// buildCrier), so the access-log record sits buffered until either
	// that window elapses or something forces a flush. cancel() here
	// drives run()'s real shutdown path, which reaches shutdownCrier
	// (closeAll, before closeDB) and therefore crier.Shutdown — whose
	// first act is closing the buffer, which per its own doc comment
	// wakes blocked consumers "so they drain instead of waiting out a
	// window". Checking receivedCh first would make this test's own
	// runtime a coin flip against that same 5s window instead of proving
	// anything about the shutdown path.
	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("run() error = %v, want nil after a clean shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not return within 10s of ctx cancellation")
	}

	// shutdownCrier must have run as part of closeAll, before run()
	// returned — this is what proves crier.Shutdown is actually reached
	// from the real shutdown path, not just callable in isolation.
	if !strings.Contains(out.String(), "crier drain completed") {
		t.Errorf(`expected an INFO "crier drain completed" line in run()'s own log output, got: %s`, out.String())
	}

	// /health is public and outside RequestID? No — RequestID wraps the
	// whole rootHandler chain, /health included (see newServer's
	// middleware.Chain), so this request does carry one; only auth is
	// what /health skips. By now the drain above has already forced the
	// flush, so the record is either already on receivedCh or arriving
	// within the HTTP round trip that follows — no more waiting out the
	// batch window.
	var accessLogAttrs map[string]string
	deadline := time.After(5 * time.Second)
	for accessLogAttrs == nil {
		select {
		case req := <-receivedCh:
			if attrs := allLogRecordAttrs(req); attrs["path"] == "/health" && attrs["method"] == "GET" {
				accessLogAttrs = attrs
			}
		case <-deadline:
			t.Fatal("collector never received the /health access log record even after a clean drain")
		}
	}

	if accessLogAttrs["request_id"] == "" {
		t.Error(`mirrored access log record has no "request_id" attribute`)
	}
	if accessLogAttrs["status"] != "200" {
		t.Errorf(`mirrored access log record "status" = %q, want "200"`, accessLogAttrs["status"])
	}
}

// --- shutdownCrier / logDrainSummary ---

func TestShutdownCrier_NilCrier_NoOp(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	if err := shutdownCrier(context.Background(), nil, logger); err != nil {
		t.Errorf("shutdownCrier(nil) error = %v, want nil", err)
	}
	if buf.Len() != 0 {
		t.Errorf("shutdownCrier(nil) logged something, want silence: %s", buf.String())
	}
}

func TestShutdownCrier_CleanDrain_LogsInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	exp := &blackholeExporter{}
	c, err := core.New(core.Options{
		ServiceName: "test",
		Exporters:   map[string]core.Exporter{"blackhole": exp},
	})
	if err != nil {
		t.Fatalf("core.New() unexpected error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdownCrier(ctx, c, logger); err != nil {
		t.Fatalf("shutdownCrier() unexpected error: %v", err)
	}

	if !strings.Contains(buf.String(), `"msg":"crier drain completed"`) {
		t.Errorf("expected an INFO \"crier drain completed\" line, got: %s", buf.String())
	}
	if strings.Contains(buf.String(), "WARN") {
		t.Errorf("clean drain must not log at WARN: %s", buf.String())
	}
}

// TestLogDrainSummary_Lost_LogsWarn exercises the WARN branch directly
// against a hand-built core.DrainSummary, rather than racing crier's real
// dispatcher to produce Lost > 0 on demand within a bounded test — see
// logDrainSummary's doc comment for why the branching is split out for
// exactly this reason.
func TestLogDrainSummary_Lost_LogsWarn(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	logDrainSummary(logger, core.DrainSummary{
		Lost:         3,
		Duration:     time.Second,
		Destinations: []string{"otlp"},
	})

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("logDrainSummary output is not valid JSON: %v\n%s", err, buf.String())
	}
	if line["level"] != "WARN" {
		t.Errorf("level = %v, want WARN when records were lost", line["level"])
	}
	if line["lost"] != float64(3) {
		t.Errorf(`"lost" attribute = %v, want 3`, line["lost"])
	}
}

func TestLogDrainSummary_Clean_LogsInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	logDrainSummary(logger, core.DrainSummary{Lost: 0, Duration: time.Millisecond})

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("logDrainSummary output is not valid JSON: %v\n%s", err, buf.String())
	}
	if line["level"] != "INFO" {
		t.Errorf("level = %v, want INFO for a clean drain", line["level"])
	}
}

// blackholeExporter accepts every batch and drops it, successfully, with
// no observable side effect — everything TestShutdownCrier_CleanDrain_LogsInfo
// needs from an exporter it never inspects.
type blackholeExporter struct{}

func (blackholeExporter) Export(context.Context, []core.LogRecord) error { return nil }
func (blackholeExporter) Shutdown(context.Context) error                 { return nil }

// --- crierTeeHandler ---

// captureExporter records every batch it is handed, safe for concurrent
// use since the dispatcher may call Export from its own goroutine while a
// test still holds the reference.
type captureExporter struct {
	mu      sync.Mutex
	batches [][]core.LogRecord
}

func (c *captureExporter) Export(_ context.Context, batch []core.LogRecord) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.batches = append(c.batches, batch)
	return nil
}

func (c *captureExporter) Shutdown(context.Context) error { return nil }

func (c *captureExporter) all() []core.LogRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []core.LogRecord
	for _, b := range c.batches {
		out = append(out, b...)
	}
	return out
}

// TestCrierTeeHandler_PreservesWrappedOutput_AndMirrorsToCrier is the
// central regression test for this file. It pins two properties at once:
//
//  1. crier is additive, never a replacement — the wrapped handler (stdout,
//     in production) sees exactly what it would without crier configured.
//  2. The "error" attribute every "unexpected service error" log site in
//     this codebase passes survives as its full text in the mirrored
//     record. Verified by experiment (not by reading crier's source) that
//     without crierAttrValue's conversion, crier's own Limits stage
//     silently replaces a raw error value with an "unsupported value
//     type" marker before export — discarding exactly the field a
//     person opens the log to read. This test fails immediately if that
//     conversion regresses.
func TestCrierTeeHandler_PreservesWrappedOutput_AndMirrorsToCrier(t *testing.T) {
	var stdout bytes.Buffer
	wrapped := slog.NewJSONHandler(&stdout, nil)

	exp := &captureExporter{}
	c, err := core.New(core.Options{
		ServiceName: "test",
		Exporters:   map[string]core.Exporter{"capture": exp},
		BatchWindow: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("core.New() unexpected error: %v", err)
	}

	logger := slog.New(newCrierTeeHandler(wrapped, c))

	wantErr := errors.New("boom: db is unreachable")
	logger.Error("unexpected service error",
		"error", wantErr,
		"request_id", "req-42",
		"method", "GET",
		"path", "/v1/tasks",
	)

	// stdout must be unaffected: same JSON line a bare slog.NewJSONHandler
	// would have produced.
	if !strings.Contains(stdout.String(), `"msg":"unexpected service error"`) {
		t.Errorf("wrapped handler output missing the log line: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), wantErr.Error()) {
		t.Errorf("wrapped handler output missing the error text: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"request_id":"req-42"`) {
		t.Errorf("wrapped handler output missing request_id: %s", stdout.String())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	summary, err := c.Shutdown(shutdownCtx)
	if err != nil {
		t.Fatalf("Shutdown() unexpected error: %v", err)
	}
	if !summary.Clean() {
		t.Fatalf("Shutdown() summary = %v, want a clean drain", summary)
	}

	records := exp.all()
	if len(records) != 1 {
		t.Fatalf("crier exported %d record(s), want exactly 1", len(records))
	}
	rec := records[0]

	if rec.Body != "unexpected service error" {
		t.Errorf("exported Body = %q, want the log message", rec.Body)
	}
	if got := rec.Attributes["request_id"]; got != "req-42" {
		t.Errorf(`exported "request_id" attribute = %v, want "req-42"`, got)
	}
	if got := rec.Attributes["error"]; got != wantErr.Error() {
		t.Errorf(`exported "error" attribute = %v, want %q (the regression this test exists for: an unconverted error value is silently replaced with an "unsupported value type" marker)`, got, wantErr.Error())
	}
	if got := rec.Attributes["method"]; got != "GET" {
		t.Errorf(`exported "method" attribute = %v, want "GET"`, got)
	}
}

func TestCrierTeeHandler_WithAttrs_MergesIntoMirroredRecord(t *testing.T) {
	var stdout bytes.Buffer
	wrapped := slog.NewJSONHandler(&stdout, nil)

	exp := &captureExporter{}
	c, err := core.New(core.Options{
		ServiceName: "test",
		Exporters:   map[string]core.Exporter{"capture": exp},
		BatchWindow: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("core.New() unexpected error: %v", err)
	}

	logger := slog.New(newCrierTeeHandler(wrapped, c)).With("component", "test-suite")
	logger.Info("hello")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() unexpected error: %v", err)
	}

	records := exp.all()
	if len(records) != 1 {
		t.Fatalf("crier exported %d record(s), want exactly 1", len(records))
	}
	if got := records[0].Attributes["component"]; got != "test-suite" {
		t.Errorf(`exported "component" attribute (bound via logger.With) = %v, want "test-suite"`, got)
	}
}

// --- crierAttrValue ---

func TestCrierAttrValue(t *testing.T) {
	tests := []struct {
		name string
		in   slog.Value
		want any
	}{
		{"string", slog.StringValue("hello"), "hello"},
		{"bool", slog.BoolValue(true), true},
		{"int64", slog.Int64Value(42), int64(42)},
		{"duration", slog.DurationValue(3 * time.Second), 3 * time.Second},
		{"error, stringified not dropped", slog.AnyValue(errors.New("db timeout")), "db timeout"},
		{"wrapped error, stringified not dropped", slog.AnyValue(fmt.Errorf("wrapped: %w", errors.New("root cause"))), "wrapped: root cause"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := crierAttrValue(tt.in)
			if got != tt.want {
				t.Errorf("crierAttrValue(%v) = %#v (%T), want %#v (%T)", tt.in, got, got, tt.want, tt.want)
			}
		})
	}
}

// --- expvar wiring (11.8) ---

func TestPublishCrierExpvarOnce_ReflectsCurrentCrier(t *testing.T) {
	// buildCrier's own currentCrierInstance/currentCrierMetrics pointers
	// are process-global (see publishCrierExpvarOnce's doc comment), so
	// this test does not run in parallel with anything else in this
	// package that also calls buildCrier — the default for this suite.

	if _, err := buildCrier(config.Config{}); err != nil {
		t.Fatalf("buildCrier(disabled) unexpected error: %v", err)
	}

	depthVar := expvar.Get("crier_buffer_depth")
	if depthVar == nil {
		t.Fatal(`expvar.Get("crier_buffer_depth") = nil; buildCrier must publish it on first call regardless of whether crier ends up enabled`)
	}
	if got := depthVar.String(); got != "0" {
		t.Errorf("crier_buffer_depth with crier disabled = %s, want 0", got)
	}

	droppedVar := expvar.Get("crier_records_dropped")
	if droppedVar == nil {
		t.Fatal(`expvar.Get("crier_records_dropped") = nil`)
	}
	var dropped map[string]int64
	if err := json.Unmarshal([]byte(droppedVar.String()), &dropped); err != nil {
		t.Fatalf("crier_records_dropped is not valid JSON: %v (%s)", err, droppedVar.String())
	}
	if len(dropped) != len(crierDropReasons) {
		t.Errorf("crier_records_dropped has %d reasons, want %d (one per core.DropReason)", len(dropped), len(crierDropReasons))
	}
	for _, reason := range crierDropReasons {
		if n, ok := dropped[string(reason)]; !ok || n != 0 {
			t.Errorf("crier_records_dropped[%q] = %v, ok=%v; want 0, true with crier disabled", reason, n, ok)
		}
	}

	// Now build an enabled crier and confirm the same expvar entries read
	// through to *this* instance rather than staying frozen at whatever
	// buildCrier(disabled) last stored.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := buildCrier(config.Config{CrierOTLPEndpoint: srv.URL})
	if err != nil {
		t.Fatalf("buildCrier(enabled) unexpected error: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = c.Shutdown(ctx)
		// Restore the disabled state so any test running later in this
		// binary reads a clean slate rather than this test's leftovers.
		_, _ = buildCrier(config.Config{})
	})

	if got, want := depthVar.String(), fmt.Sprint(c.Depth()); got != want {
		t.Errorf("crier_buffer_depth = %s, want %s (c.Depth(), read live through the current instance)", got, want)
	}
}
