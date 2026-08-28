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

// RequireAuth returns a middleware.Middleware that rejects any request
// without a valid "Authorization: Bearer <token>" header, and on success
// stores the authenticated user's ID and the raw token in the request
// context (via middleware.ContextWithUserID / ContextWithSessionToken)
// before calling the wrapped handler.
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
			token, ok := bearerToken(r)
			if !ok {
				writeAuthError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
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
