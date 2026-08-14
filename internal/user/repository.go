package user

import (
	"context"
	"time"
)

// Repository defines the contract for user and session persistence. It
// bundles both because they're always backed by the same store in
// practice (one *sql.DB, or one in-process map set) and a session cannot
// meaningfully exist without a user — splitting them into two interfaces
// would not buy any real independence, only more wiring.
//
// Session methods never return ErrAlreadyExists: a session's TokenHash is
// derived from cryptographically random bytes (see Service.CreateSession),
// so a collision is not a case any caller needs to handle.
type Repository interface {
	CreateUser(ctx context.Context, u User) error
	FindUserByEmail(ctx context.Context, email string) (User, error)
	FindUserByID(ctx context.Context, id string) (User, error)

	CreateSession(ctx context.Context, s Session) error
	FindSessionByTokenHash(ctx context.Context, tokenHash string) (Session, error)
	DeleteSession(ctx context.Context, tokenHash string) error

	// DeleteExpiredSessions removes every session whose ExpiresAt is
	// before now. See Service.PruneExpiredSessions — the only caller —
	// for why this exists in addition to ValidateToken's own lazy,
	// per-token expiry check.
	DeleteExpiredSessions(ctx context.Context, now time.Time) error
}
