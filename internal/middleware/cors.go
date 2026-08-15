package middleware

import "net/http"

// corsAllowedHeaders and corsAllowedMethods are sent on every preflight
// response CORS answers. Authorization is listed because bearer tokens
// are sent that way (see user.RequireAuth) — without it, a browser's
// preflight check rejects the actual request before this API ever sees
// it. X-Request-Id is listed so a browser client may set it explicitly to
// correlate its own logs with the server's (see RequestID), the same way
// a server-to-server caller already can.
const (
	corsAllowedHeaders = "Authorization, Content-Type, X-Request-Id"
	corsAllowedMethods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"
	corsMaxAge         = "600"
)

// CORS returns a Middleware that adds Cross-Origin Resource Sharing
// headers for browser clients whose origin is in allowedOrigins, and
// answers CORS preflight (OPTIONS) requests directly.
//
// Disabled by default: an empty allowedOrigins (the zero value — see
// config.Config.CORSAllowedOrigins) makes every request fall straight
// through with no headers added and no behavior change from before this
// middleware existed. This matches every other opt-in setting in this
// codebase — a server-to-server or same-origin client (the only kind this
// API served until now) needs none of this.
//
// Preflight requests must be answered before routing, not inside a
// handler: a route registered only for GET/POST/etc. (see
// task.Handler.RegisterRoutes) has no OPTIONS handler of its own, so an
// unhandled preflight would 404 rather than succeed. CORS must therefore
// wrap the whole mux and intercept OPTIONS itself — see its position in
// cmd/api/main.go's middleware chain.
//
// Only an explicitly allowed origin ever gets a response header: this is
// not a wildcard "*" implementation. A caller from an origin not in the
// list sees the same response it always would have (typically a 404 on
// its OPTIONS preflight, or a same-origin-policy failure on the browser
// side reading the actual response) — CORS being enabled for one origin
// never widens what any other origin can do.
func CORS(allowedOrigins []string) Middleware {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(allowed) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			origin := r.Header.Get("Origin")
			if origin == "" || !allowed[origin] {
				next.ServeHTTP(w, r)
				return
			}

			// Vary: Origin tells any cache in front of this server that the
			// response depends on the request's Origin header, so it never
			// serves an Access-Control-Allow-Origin meant for one origin to
			// a browser from another.
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")

			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", corsAllowedMethods)
				w.Header().Set("Access-Control-Allow-Headers", corsAllowedHeaders)
				w.Header().Set("Access-Control-Max-Age", corsMaxAge)
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
