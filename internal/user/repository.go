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

	// CreateSession stores s and evicts s.UserID's oldest sessions (by
	// CreatedAt) past maxSessions, both inside one transaction.
	//
	// The eviction query itself is self-correcting against whatever the
	// table actually holds at the moment it runs — it is not "read a
	// count, then decide", so two logins racing each other converge to
	// the same correct outcome regardless of interleaving; a concurrency
	// test confirmed this holds even for a deliberately non-transactional
	// version with an artificial delay between insert and evict (see
	// docs/DECISIONS.md § "Limite de sessões: teto com evicção da mais
	// antiga" for that finding in full — it's why the transaction here is
	// justified by partial-failure atomicity, not by a race the eviction
	// query wins on its own anyway).
	//
	// What the transaction actually buys: if the eviction half fails —
	// a timeout, a dropped connection — after the insert half already
	// committed, an un-transacted version would leave the new session
	// stored and never evaluated for eviction, the one way this cap can
	// still be defeated over many such failures. The transaction makes
	// insert-then-evict succeed or fail together.
	//
	// maxSessions <= 0 is a programming error, not "unlimited" — every
	// caller has a real Config.AuthMaxSessionsPerUser to pass; the
	// zero value of an unset test config is exactly the case this
	// should not silently accept as "no cap".
	CreateSession(ctx context.Context, s Session, maxSessions int) error

	FindSessionByTokenHash(ctx context.Context, tokenHash string) (Session, error)
	DeleteSession(ctx context.Context, tokenHash string) error

	// DeleteSessionsForUser removes every session belonging to userID,
	// unconditionally — including the one that authenticated the
	// request that led here. Backs Service.LogoutAll: a caller who
	// suspects a stolen token gets one operation that guarantees every
	// live session is gone, active one included, rather than having to
	// enumerate and delete them individually.
	DeleteSessionsForUser(ctx context.Context, userID string) error

	// DeleteExpiredSessions removes every session whose ExpiresAt is
	// before now. See Service.PruneExpiredSessions — the only caller —
	// for why this exists in addition to ValidateToken's own lazy,
	// per-token expiry check.
	DeleteExpiredSessions(ctx context.Context, now time.Time) error
}
