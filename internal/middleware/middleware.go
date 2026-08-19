// Package middleware provides small, composable net/http middlewares
// (RequestID, Logging, Recovery, SecurityHeaders, CORS, RateLimiter) for
// the HTTP layer, plus generic request context plumbing (see
// auth_context.go). It has no knowledge of the application's domain — it
// never imports internal/task or internal/user — and works exclusively in
// terms of net/http, context and log/slog.
// Domain-aware middleware (e.g. user.RequireAuth) lives beside the domain
// it belongs to and calls into this package's context helpers, not the
// other way around.
package middleware

import "net/http"

// Middleware wraps an http.Handler to add cross-cutting behavior (request
// ID assignment, logging, panic recovery, ...) without changing what the
// wrapped handler does.
type Middleware func(http.Handler) http.Handler

// Chain composes middlewares into a single Middleware, in the order given:
// Chain(a, b, c)(h) is equivalent to a(b(c(h))). A request therefore runs
// a's logic first, then b's, then c's, then h; on the way out it unwinds
// in the opposite order — h, then c, then b, then a.
func Chain(middlewares ...Middleware) Middleware {
	return func(final http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}
