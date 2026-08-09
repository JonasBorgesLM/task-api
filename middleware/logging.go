package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

// Logging returns a Middleware that records one structured log line per
// request: HTTP method, path, response status code, duration, and the
// request ID set by RequestID (empty if RequestID is not earlier in the
// chain).
func Logging(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			requestID, _ := RequestIDFromContext(r.Context())
			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration", time.Since(start).String(),
				"request_id", requestID,
			)
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
