package user

import "time"

// User is the core domain entity for an account holder.
type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// PasswordHash is a bcrypt hash, never the plaintext password. It is
	// not part of the public wire format (json:"-"): a User is never
	// serialized with it, even accidentally, by a handler that reuses this
	// struct for a response body.
	PasswordHash string `json:"-"`
}

// Session is a server-side record of an issued bearer token, used for
// opaque (non-JWT) authentication — see Service.CreateSession and
// Service.ValidateToken. It is internal bookkeeping only, never
// serialized to a client.
type Session struct {
	// TokenHash is sha256(token), hex-encoded — the raw token itself is
	// handed to the client once, at login, and never stored (see
	// Repository's doc comment).
	TokenHash string
	UserID    string
	ExpiresAt time.Time
	CreatedAt time.Time
}
