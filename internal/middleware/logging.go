package middleware

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Logging returns a Middleware that records exactly one structured log
// line per request: HTTP method, path, response status, duration, and the
// request ID set by RequestID (empty if RequestID is not earlier in the
// chain).
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
func Logging(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

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

// HeaderWritten reports whether WriteHeader (directly, or implicitly via
// the first Write — see Write below) has already run. Recovery uses this
// to decide whether it may still write its own error response after a
// panic, or whether the handler already committed one.
func (rec *statusRecorder) HeaderWritten() bool {
	return rec.wroteHeader
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
