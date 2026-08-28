package middleware

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Logging returns a Middleware that records exactly one structured log
// line per request: HTTP method, path, response status, duration, the
// request ID set by RequestID (empty if RequestID is not earlier in the
// chain), and — when the request authenticated — the caller's user ID.
//
// The log level reflects the outcome — Info for 2xx/3xx, Warn for 4xx,
// Error for 5xx — and an "error" field (the HTTP status text) is added
// whenever the status indicates a failure. That lets a log pipeline
// identify failed requests by filtering on level or on the presence of
// "error", without parsing the numeric status.
//
// Request and response bodies are never logged: they may carry task
// titles/descriptions that a caller wouldn't expect to end up in server
// logs, and logging them would add far more risk than diagnostic value.
//
// # Why user_id needs a pointer, not a second context.WithValue
//
// Ownership is the invariant this whole codebase is built around, so
// "which user touched this request" belongs on the one log line every
// request produces. The obstacle is ordering: authentication
// (user.RequireAuth) is wired per-route, inside each domain's
// RegisterRoutes — see cmd/api/main.go's newServer — so it runs *after*
// Logging's own next.ServeHTTP call is already in flight, not before it.
// By the time RequireAuth calls context.WithValue to add the user ID,
// Logging is holding a *http.Request from earlier in the chain, and
// context.WithValue returns a *new* context bound to a *new* Request —
// visible only to whatever RequireAuth itself calls next, never to the
// outer frame already waiting on next.ServeHTTP to return.
//
// A pointer sidesteps that: Logging stashes one, empty, in the context
// before calling next. context.WithValue's *binding* can't be observed
// from the outside after the fact, but the string the pointer targets
// can still be written through it — which is exactly what
// RecordUserIDForLog (auth_context.go) does from inside RequireAuth.
// Safe without synchronization because a single request's middleware
// chain runs sequentially on one goroutine; nothing here fans out.
type userIDForLogKeyType struct{}

var userIDForLogKey = userIDForLogKeyType{}

// contextWithUserIDForLog returns a copy of ctx carrying a pointer to an
// empty string, and that same pointer. See Logging's doc comment for why
// a pointer, rather than a value read back later with context.Value, is
// what this needs.
func contextWithUserIDForLog(ctx context.Context) (context.Context, *string) {
	userID := new(string)
	return context.WithValue(ctx, userIDForLogKey, userID), userID
}

func Logging(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			ctx, userID := contextWithUserIDForLog(r.Context())
			r = r.WithContext(ctx)

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			requestID, _ := RequestIDFromContext(r.Context())
			attrs := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration", time.Since(start).String(),
				"request_id", requestID,
			}
			if *userID != "" {
				attrs = append(attrs, "user_id", *userID)
			}

			level := slog.LevelInfo
			switch {
			case rec.status >= http.StatusInternalServerError:
				level = slog.LevelError
			case rec.status >= http.StatusBadRequest:
				level = slog.LevelWarn
			}
			if rec.status >= http.StatusBadRequest {
				attrs = append(attrs, "error", http.StatusText(rec.status))
			}

			logger.Log(r.Context(), level, "http request", attrs...)
		})
	}
}

// statusRecorder wraps http.ResponseWriter to capture the status code
// written by the wrapped handler, since http.ResponseWriter doesn't expose
// it directly.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (rec *statusRecorder) WriteHeader(code int) {
	if !rec.wroteHeader {
		rec.status = code
		rec.wroteHeader = true
	}
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *statusRecorder) Write(b []byte) (int, error) {
	// http.ResponseWriter.Write implicitly sends a 200 status if
	// WriteHeader hasn't been called yet — mirror that here so the
	// recorded status always reflects what was actually sent to the
	// client.
	if !rec.wroteHeader {
		rec.WriteHeader(http.StatusOK)
	}
	return rec.ResponseWriter.Write(b)
}

// Flush implements http.Flusher by delegating to the wrapped
// ResponseWriter when it supports flushing, and is a no-op otherwise.
// Without this, a handler wrapped by Logging that type-asserts its
// http.ResponseWriter to http.Flusher (e.g. for streaming/SSE responses)
// would see the assertion fail even when the underlying writer actually
// supports it — struct embedding only promotes methods declared on the
// embedded interface (http.ResponseWriter), not http.Flusher.
func (rec *statusRecorder) Flush() {
	if f, ok := rec.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack implements http.Hijacker by delegating to the wrapped
// ResponseWriter when it supports hijacking (e.g. for a WebSocket
// upgrade), and returns an error otherwise, matching the contract
// documented on http.Hijacker.
func (rec *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := rec.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("middleware: underlying ResponseWriter does not implement http.Hijacker")
	}
	return h.Hijack()
}

// Push implements http.Pusher by delegating to the wrapped ResponseWriter
// when it supports HTTP/2 server push, and returns http.ErrNotSupported
// otherwise, matching the contract documented on http.Pusher.
func (rec *statusRecorder) Push(target string, opts *http.PushOptions) error {
	p, ok := rec.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return p.Push(target, opts)
}
