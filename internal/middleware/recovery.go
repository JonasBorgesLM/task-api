package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recovery returns a Middleware that recovers from panics in the handlers
// it wraps, logs the panic value and stack trace internally, and responds
// to the client with a generic HTTP 500. The panic value and stack trace
// are never included in the response.
func Recovery(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				panicValue := recover()
				if panicValue == nil {
					return
				}

				if panicValue == http.ErrAbortHandler {
					// http.ErrAbortHandler is net/http's own sentinel for
					// intentionally aborting a handler (e.g. the client
					// hung up mid-request); re-panic so the server's own
					// machinery can handle it as documented, instead of
					// treating it as an application error.
					panic(panicValue)
				}

				requestID, _ := RequestIDFromContext(r.Context())
				logger.Error("panic recovered",
					"panic", panicValue,
					"stack", string(debug.Stack()),
					"method", r.Method,
					"path", r.URL.Path,
					"request_id", requestID,
				)

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal server error"}`))
			}()

			next.ServeHTTP(w, r)
		})
	}
}
