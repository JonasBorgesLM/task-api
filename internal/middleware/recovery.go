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
//
// If the handler had already written a response before it panicked —
// possible with a streaming or partially-written response, and the
// reason http.Server itself checks this before logging its own "superfluous
// response.WriteHeader" warning — Recovery does not attempt to write a
// second one. Writing anyway would produce exactly that warning and
// concatenate a second, invalid body onto whatever the client already
// started receiving; the client ends up with neither a valid original
// response nor a valid error one. In that case Recovery only logs.
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

				// responseStarted reports whether some prior middleware
				// (in this chain, Logging's statusRecorder — see
				// logging.go) observed a write already happen. Recovery
				// runs innermost in the chain (cmd/api/main.go), so by
				// the time a request reaches it, w has already passed
				// through Logging's wrapper on the way in; a plain
				// http.ResponseWriter (e.g. in a test that exercises
				// Recovery alone) simply reports false, preserving the
				// original behavior for that case.
				started := responseStarted(w)

				requestID, _ := RequestIDFromContext(r.Context())
				logger.Error("panic recovered",
					"panic", panicValue,
					"stack", string(debug.Stack()),
					"method", r.Method,
					"path", r.URL.Path,
					"request_id", requestID,
					"response_already_started", started,
				)

				if started {
					return
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal server error"}`))
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// headerWritten is satisfied by statusRecorder (see logging.go).
// responseStarted checks for it with a plain type assertion: Recovery
// only ever runs inside the chain this package's own Logging builds
// (cmd/api/main.go), so there is exactly one wrapper to know about, and
// no case today where a second one sits in between.
type headerWritten interface {
	HeaderWritten() bool
}

// responseStarted reports whether the handler already wrote to w before
// panicking. A plain http.ResponseWriter (e.g. in a test exercising
// Recovery in isolation, without Logging) reports false, which is the
// same as this check not existing — the original, always-write
// behavior.
func responseStarted(w http.ResponseWriter) bool {
	hw, ok := w.(headerWritten)
	return ok && hw.HeaderWritten()
}
