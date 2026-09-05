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

	// UpdateUserPassword replaces id's stored password hash and bumps
	// updated_at to now. Returns ErrNotFound if no user with that id
	// exists. Backs Service.ChangePassword — never called with a
	// plaintext password, only the bcrypt hash of one.
	UpdateUserPassword(ctx context.Context, id, passwordHash string) error

	// DeleteUser removes the user row itself. Returns ErrNotFound if no
	// user with that id exists. Backs Service.DeleteAccount, and must be
	// called only after every task the user owns is already gone:
	// tasks.user_id has no ON DELETE CASCADE (unlike sessions.user_id —
	// see 0004_add_user_id_to_tasks.up.sql's own comment), so
	// postgresRepository's implementation errors instead of silently
	// leaving orphaned tasks behind if any still reference this id.
	DeleteUser(ctx context.Context, id string) error

	// CreateSession stores s and evicts s.UserID's oldest sessions (by
	// CreatedAt) past maxSessions, inside one transaction serialized per
	// user by a PostgreSQL advisory lock — see postgresRepository.
	// CreateSession's doc comment for exactly why the lock is load-
	// bearing, not a belt-and-suspenders addition.
	//
	// The short version: a transaction around insert+evict is not
	// enough on its own. PostgreSQL's default isolation (READ COMMITTED)
	// lets each of several concurrent CreateSession transactions see
	// only what was already committed *before its own SELECT ran*, plus
	// its own just-inserted row — so ten logins arriving close together
	// can each conclude, independently and correctly by its own limited
	// view, that eviction isn't needed yet. This was caught by CI, not
	// locally: an earlier version without the lock passed a local
	// concurrency test repeatedly and left 7 sessions instead of 3 the
	// one time it ran against a CI runner's real network latency. See
	// docs/DECISIONS.md § "Limite de sessões: teto com evicção da mais
	// antiga" for that finding in full.
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

	// DeleteSessionsForUserExcept removes every session belonging to
	// userID other than the one whose hash is keepTokenHash. Backs
	// Service.ChangePassword: rotating the credential should not also
	// sign out the session that just proved it knows the current
	// password, only every *other* one — the opposite split from
	// DeleteSessionsForUser, which is unconditional on purpose (see its
	// own doc comment) because LogoutAll has no session left to spare.
	DeleteSessionsForUserExcept(ctx context.Context, userID, keepTokenHash string) error

	// DeleteExpiredSessions removes every session whose ExpiresAt is
	// before now. See Service.PruneExpiredSessions — the only caller —
	// for why this exists in addition to ValidateToken's own lazy,
	// per-token expiry check.
	DeleteExpiredSessions(ctx context.Context, now time.Time) error
}
