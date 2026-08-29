package user

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/JonasBorgesLM/task-api/internal/middleware"
)

// sessionValidator is the interface RequireAuth depends on, so it can be
// tested with a fake instead of a real Service — the same
// smallest-possible-interface pattern as Handler's userService.
type sessionValidator interface {
	ValidateToken(ctx context.Context, token string) (userID string, err error)
}

// sessionCookieName is the cookie Handler.login sets and this middleware
// reads back, carrying the same opaque token Authorization: Bearer would
// (see docs/DECISIONS.md § "Autenticação: modo duplo (cookie httpOnly +
// Bearer)"). Defined here, alongside the reader, rather than in
// handler.go alongside the writer — RequireAuth is what makes the cookie
// meaningful; handler.go's Set-Cookie references this same constant.
const sessionCookieName = "session_token"

// credentialSource reports which of the two accepted transports r
// presents a session credential on — "bearer", "cookie", or "" when
// neither is present — and the token it carries.
//
// Authorization takes precedence when both are present: it is explicit
// and deliberate on the caller's part, while the cookie is attached by
// the browser automatically without the caller choosing to. A request
// resolved this way is the Bearer path for every purpose downstream,
// CSRF included — this is deliberately the same question a CSRF gate
// needs answered (does this request carry Authorization?), so it is
// factored out here rather than duplicated in a second place that could
// drift from this one.
//
// A cookie present but empty is treated the same as absent, not as a
// distinct malformed-credential case: an empty token would fail
// ValidateToken as unknown anyway, so there is no separate failure mode
// worth preserving here.
func credentialSource(r *http.Request) (source, token string) {
	if t, ok := bearerToken(r); ok {
		return "bearer", t
	}
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		return "cookie", c.Value
	}
	return "", ""
}

// RequireAuth returns a middleware.Middleware that rejects any request
// without a valid session credential — "Authorization: Bearer <token>" or
// the session cookie (see credentialSource for how the two are resolved
// when both are present) — and on success stores the authenticated
// user's ID and the raw token in the request context (via
// middleware.ContextWithUserID / ContextWithSessionToken) before calling
// the wrapped handler.
//
// It distinguishes two very different reasons a request can fail here,
// because collapsing them into one status is actively harmful:
//
//   - ErrNotFound — the token is unknown or expired. That is a statement
//     about the caller's credential, so it is 401, and it is not logged
//     (a routine outcome; the access log line already records the status).
//
//   - anything else — the session lookup itself failed (PostgreSQL
//     unreachable, a query timeout, a canceled context). That says nothing
//     about the caller's credential: a perfectly valid token produces this
//     too. Answering 401 there would tell a client its credential is bad
//     when it isn't, prompting well-behaved clients to discard a working
//     token and stampede the login endpoint — which needs the same
//     unavailable database. It would also hide the outage: 401 is logged
//     at WARN by the Logging middleware, so a total database failure on
//     every authenticated route would surface as a spike in "client auth
//     errors" rather than as server errors, and 5xx-based alerting would
//     never fire. So this returns 503 (matching GET /health/ready, which
//     already reports 503 for exactly this condition) and logs the
//     underlying cause once, at Error level, with the request ID — the
//     same treatment Handler.handleServiceError gives an unexpected error.
//
// It lives in this package rather than in the generic middleware package
// so that middleware itself never needs to know a Service or a token
// scheme exist — see middleware's package doc comment. task.Handler reads
// the resulting user ID back out with middleware.UserIDFromContext.
func RequireAuth(svc sessionValidator, logger *slog.Logger) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, token := credentialSource(r)
			if token == "" {
				writeAuthError(w, http.StatusUnauthorized, "missing or invalid session credential")
				return
			}

			userID, err := svc.ValidateToken(r.Context(), token)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					writeAuthError(w, http.StatusUnauthorized, "invalid or expired session token")
					return
				}

				requestID, _ := middleware.RequestIDFromContext(r.Context())
				logger.Error("session lookup failed",
					"error", err,
					"request_id", requestID,
					"method", r.Method,
					"path", r.URL.Path,
				)
				writeAuthError(w, http.StatusServiceUnavailable, "service temporarily unavailable, please retry")
				return
			}

			// Called before ctx is built below, and with r.Context()
			// rather than ctx: either would actually work here (ctx's
			// parent is r.Context(), and context.Value walks up to find
			// a key it doesn't own), but reading r.Context() directly is
			// what stays correct if a future change reorders these two
			// lines. See middleware.RecordUserIDForLog's doc comment for
			// why this call exists at all.
			middleware.RecordUserIDForLog(r.Context(), userID)

			ctx := middleware.ContextWithUserID(r.Context(), userID)
			ctx = middleware.ContextWithSessionToken(ctx, token)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// bearerToken extracts the token from a "Authorization: Bearer <token>"
// header, reporting false if the header is absent or doesn't match that
// exact scheme.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "

	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, prefix) {
		return "", false
	}

	token := strings.TrimPrefix(auth, prefix)
	if token == "" {
		return "", false
	}
	return token, true
}

// writeAuthError writes status with the same {"error": "..."} envelope
// Handler uses, for the requests that never reach a Handler method at all
// (rejected here, in the middleware).
func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
