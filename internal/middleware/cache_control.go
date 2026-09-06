package middleware

import (
	"net/http"
	"strings"
)

// CacheControl sets Cache-Control on every response next produces, and
// nothing else — no imported domain package, only a path prefix. This
// API is JSON-only and every 2xx it returns belongs to one authenticated
// caller, so nothing it serves should be storable by a shared cache or
// replayed to anyone but the request that produced it: a bare 200 with
// no Cache-Control is heuristically cacheable under RFC 9111 §4.2.2, and
// without private/no-store/no-cache a proxy in front of this API is free
// to do exactly that.
//
// authPrefix paths (compared with strings.HasPrefix, so it must be given
// the same unprefixed pattern a Handler.RegisterRoutes call uses, not
// the "/v1/..." one a client sends — see this middleware's mounting
// point in cmd/api/newServer) get "private, no-store": a login response
// carries a session token, and nothing about it should be written to
// disk or kept in memory beyond the single response that returned it.
//
// Everything else gets "private, no-cache" — still allows a client to
// keep a copy for conditional revalidation later, but never lets it
// serve that copy without asking again, and "private" keeps a shared
// cache (a CDN, a corporate proxy) from serving one user's response to
// a second one who happens to share that intermediary.
func CacheControl(authPrefix string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, authPrefix) {
				w.Header().Set("Cache-Control", "private, no-store")
			} else {
				w.Header().Set("Cache-Control", "private, no-cache")
			}
			next.ServeHTTP(w, r)
		})
	}
}
