package middleware

import "context"

// This file holds only generic context plumbing — like request_id.go's
// context helpers — never anything that knows what a "user" or a
// "session" is. That knowledge lives in user.RequireAuth (which calls
// these setters) and task.Handler (which calls the getters); middleware
// itself never imports either domain package, per this package's doc
// comment.

type userIDKeyType struct{}
type sessionTokenKeyType struct{}

var (
	userIDKey       = userIDKeyType{}
	sessionTokenKey = sessionTokenKeyType{}
)

// ContextWithUserID returns a copy of ctx carrying userID, retrievable
// with UserIDFromContext.
func ContextWithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// UserIDFromContext returns the user ID stored in ctx by
// ContextWithUserID, and false if none is present (e.g. the request never
// passed through an auth-requiring middleware).
func UserIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(userIDKey).(string)
	return id, ok
}

// ContextWithSessionToken returns a copy of ctx carrying the raw bearer
// token that authenticated the request, retrievable with
// SessionTokenFromContext.
func ContextWithSessionToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, sessionTokenKey, token)
}

// SessionTokenFromContext returns the raw bearer token stored in ctx by
// ContextWithSessionToken, and false if none is present.
func SessionTokenFromContext(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(sessionTokenKey).(string)
	return token, ok
}

// RecordUserIDForLog records userID so the request's access-log line
// (written by Logging — see logging.go) includes a user_id field.
//
// It is a no-op if Logging is not part of the chain in front of the
// caller — e.g. a test that exercises RequireAuth directly, without
// building the full middleware chain — so a caller never needs to check
// whether logging is wired up before calling this.
//
// Call this with the request's *original* context (the one RequireAuth
// received, before it builds the new one via ContextWithUserID) — that
// is the context Logging actually stashed the carrier pointer in. See
// Logging's doc comment for the full reasoning; user.RequireAuth is the
// one call site today.
func RecordUserIDForLog(ctx context.Context, userID string) {
	if p, ok := ctx.Value(userIDForLogKey).(*string); ok {
		*p = userID
	}
}
