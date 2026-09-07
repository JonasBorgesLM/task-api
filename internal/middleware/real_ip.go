package middleware

import (
	"context"
	"net/http"
)

// AddressKeyFunc resolves the caller's address for a single request. It
// has the exact shape moat/ratelimit.KeyFunc already does — cmd/api's own
// addressKeyFunc (the function that decides between the raw TCP peer and
// a TRUSTED_PROXIES-aware moat/realip extractor) is passed to RealIP
// directly, so there is exactly one implementation of "what is this
// request's real address" in the whole codebase, not two that could
// silently drift apart.
type AddressKeyFunc func(*http.Request) (string, error)

type realIPKeyType struct{}

var realIPKey = realIPKeyType{}

// RealIP resolves addressOf(r) once per request and stores the result in
// the request context, retrievable via RealIPFromContext — for any
// downstream code (audit logging, most concretely — see
// user.Handler.logAuditEvent) that wants the caller's address without
// itself importing moat/realip or the rate-limit key function. This
// package stays free of domain knowledge either way, the same as
// RequestID: it knows nothing about what an "audit event" is, only how to
// resolve and hand off an address.
//
// A resolution error (only possible when TRUSTED_PROXIES is configured
// and the header parse fails) stores an empty string rather than failing
// the request — this is a value for logging, never a decision anything
// downstream is gated on, so degrading to "address unknown" is strictly
// better than turning a log-enrichment concern into a 500.
func RealIP(addressOf AddressKeyFunc) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			addr, err := addressOf(r)
			if err != nil {
				addr = ""
			}
			ctx := context.WithValue(r.Context(), realIPKey, addr)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RealIPFromContext returns the address RealIP stored in ctx, or "" if
// RealIP was never part of the middleware chain for this request (e.g. a
// unit test that builds its own bare context).
func RealIPFromContext(ctx context.Context) string {
	addr, _ := ctx.Value(realIPKey).(string)
	return addr
}
