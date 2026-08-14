package user

import (
	"context"
	"encoding/json"
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
// without a valid "Authorization: Bearer <token>" header with 401, and on
// success stores the authenticated user's ID and the raw token in the
// request context (via middleware.ContextWithUserID /
// ContextWithSessionToken) before calling the wrapped handler.
//
// It lives in this package rather than in the generic middleware package
// so that middleware itself never needs to know a Service or a token
// scheme exist — see middleware's package doc comment. task.Handler reads
// the resulting user ID back out with middleware.UserIDFromContext.
func RequireAuth(svc sessionValidator) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				writeUnauthorized(w, "missing or malformed Authorization header")
				return
			}

			userID, err := svc.ValidateToken(r.Context(), token)
			if err != nil {
				writeUnauthorized(w, "invalid or expired session token")
				return
			}

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

// writeUnauthorized writes a 401 with the same {"error": "..."} envelope
// Handler uses, for the requests that never reach a Handler method at all
// (rejected here, in the middleware).
func writeUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
