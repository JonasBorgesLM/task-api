package middleware

import (
	"net/http"

	"github.com/JonasBorgesLM/moat/csrf"
)

// CSRF returns a Middleware that applies p's CSRF protection to every
// state-changing request that does not carry an Authorization header —
// see docs/DECISIONS.md § "Autenticação: modo duplo (cookie httpOnly +
// Bearer)" for the reasoning this codifies.
//
// This function does not know what a "session" or a "user" is — it only
// looks at whether the Authorization header is present, keeping this
// package free of domain knowledge (see this package's doc comment).
// That is deliberate, not a simplification of a richer check: the
// question CSRF actually needs answered — "did this request choose the
// Bearer path?" — never requires resolving a session, so this gate does
// not need to run after user.RequireAuth, or share any state with it, to
// give the right answer. user.RequireAuth asks a related but distinct
// question (which credential, if any, is valid) using its own
// credentialSource; the two independently agree on "Authorization
// present ⇒ Bearer path" because that is the one fact both need, not
// because one derives it from the other.
//
//   - Safe methods (GET, HEAD, OPTIONS, TRACE) always go through p's
//     Middleware, regardless of Authorization. A safe request never
//     changes state, so there is nothing to protect against forgery —
//     what this step actually does for a safe request is mint or
//     refresh the CSRF cookie and make csrf.Token available, which is
//     what user.Handler's GET /auth/csrf-token (see CI-5 of
//     docs/changes/dual-auth-mode/plan.md) depends on. Routing every
//     safe request through here is what makes wrapping that one route
//     with its own csrfMiddleware redundant going forward, not wrong.
//   - A mutating method (POST, PUT, PATCH, DELETE, ...) with
//     Authorization present skips this middleware entirely: a
//     server-to-server or curl client authenticates with a header a
//     browser cannot be tricked into sending cross-site, so it has
//     nothing to prove here, and every `curl` example in the README
//     must keep working unmodified.
//   - A mutating method without Authorization is assumed to be a
//     browser — cookie-authenticated, or unauthenticated and about to
//     call POST /auth/login or /auth/register — and always goes
//     through p's Middleware, which verifies Origin and the CSRF token.
//     This is what closes the "login CSRF" gap a narrower design (CSRF
//     only on already-authenticated cookie routes) would leave open.
func CSRF(p *csrf.Protector) Middleware {
	return func(next http.Handler) http.Handler {
		protected := p.Middleware(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isSafeMethod(r.Method) && r.Header.Get("Authorization") != "" {
				next.ServeHTTP(w, r)
				return
			}
			protected.ServeHTTP(w, r)
		})
	}
}

// isSafeMethod mirrors the method set csrf.Protector.Middleware itself
// treats as safe (see its own doc comment) — duplicated rather than
// imported because the package exports no such predicate, only the
// behavior built on top of it.
func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}
