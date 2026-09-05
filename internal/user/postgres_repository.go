package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// postgreSQLUniqueViolation is the PostgreSQL error code for a unique
// constraint violation (23505) — see internal/task/postgres_repository.go
// for the same constant and reasoning; duplicated here rather than shared
// because it's a single well-known string, not worth a shared package for.
const postgreSQLUniqueViolation = "23505"

// postgresRepository is a PostgreSQL implementation of Repository, backed
// by a *sql.DB shared with internal/task's postgresRepository (see
// cmd/api/main.go) — one connection pool for the whole process, since both
// domains live in the same database.
type postgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository returns a new PostgreSQL-backed Repository using
// db for all queries. Lifecycle (Close) is the caller's responsibility —
// see cmd/api/main.go.
func NewPostgresRepository(db *sql.DB) Repository {
	return &postgresRepository{db: db}
}

// CreateUser persists a new user. Returns ErrAlreadyExists if the email is
// already taken.
func (r *postgresRepository) CreateUser(ctx context.Context, u User) error {
	const query = `
		INSERT INTO users (id, email, password_hash, created_at, updated_at)
		VALUES ($1::uuid, $2, $3, $4, $5)
	`

	_, err := r.db.ExecContext(ctx, query, u.ID, u.Email, u.PasswordHash, u.CreatedAt, u.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("postgres: create user: %w", err)
	}
	return nil
}

// FindUserByEmail returns the user with the given email. Returns
// ErrNotFound if absent.
func (r *postgresRepository) FindUserByEmail(ctx context.Context, email string) (User, error) {
	const query = `
		SELECT id::text, email, password_hash, created_at, updated_at
		FROM users
		WHERE email = $1
	`
	u, err := scanUser(r.db.QueryRowContext(ctx, query, email))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("postgres: find user by email: %w", err)
	}
	return u, nil
}

// FindUserByID returns the user with the given ID. Returns ErrNotFound if
// absent.
func (r *postgresRepository) FindUserByID(ctx context.Context, id string) (User, error) {
	const query = `
		SELECT id::text, email, password_hash, created_at, updated_at
		FROM users
		WHERE id = $1::uuid
	`
	u, err := scanUser(r.db.QueryRowContext(ctx, query, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("postgres: find user by id: %w", err)
	}
	return u, nil
}

// UpdateUserPassword replaces id's stored password hash and bumps
// updated_at to now. Returns ErrNotFound if no user with that id exists.
func (r *postgresRepository) UpdateUserPassword(ctx context.Context, id, passwordHash string) error {
	const query = `
		UPDATE users
		SET password_hash = $2, updated_at = now()
		WHERE id = $1::uuid
	`
	result, err := r.db.ExecContext(ctx, query, id, passwordHash)
	if err != nil {
		return fmt.Errorf("postgres: update user password: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: update user password: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteUser removes the user row itself. Returns ErrNotFound if no user
// with that id exists.
//
// tasks.user_id references users(id) with no ON DELETE CASCADE (see
// 0004_add_user_id_to_tasks.up.sql's own comment) — deliberately, unlike
// sessions.user_id. If any task still references id, PostgreSQL itself
// refuses this DELETE with a foreign key violation rather than either
// cascading it (which would leave attachment blobs orphaned with nothing
// left to reach them through) or silently succeeding with an orphaned
// task row. That surfaces here as a wrapped, non-sentinel error — a
// caller reaching this with tasks still in place has a cascade-ordering
// bug, not a condition Service.DeleteAccount's contract is meant to
// paper over.
func (r *postgresRepository) DeleteUser(ctx context.Context, id string) error {
	const query = `DELETE FROM users WHERE id = $1::uuid`
	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("postgres: delete user: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: delete user: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateSession persists a new session.
// CreateSession inserts s and evicts s.UserID's oldest sessions past
// maxSessions. The DELETE's subquery orders by created_at DESC and keeps
// the first maxSessions, using idx_sessions_user_id_created_at
// (0008_add_sessions_user_id_created_at_index.up.sql) to do that without
// a sort step.
func (r *postgresRepository) CreateSession(ctx context.Context, s Session, maxSessions int) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres: create session: begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has succeeded

	// Serializes concurrent CreateSession calls for the *same* user —
	// without this, the transaction below is not enough. PostgreSQL's
	// default isolation (READ COMMITTED) lets each concurrent
	// transaction's evict SELECT see only rows already committed by
	// others *before that SELECT ran*, plus its own just-inserted row.
	// Ten transactions starting close together each see one session
	// (their own) — 1 <= maxSessions, so each independently concludes
	// there is nothing to evict, and all ten commit. This is not a
	// hypothetical: an earlier version without this lock passed a local
	// concurrency test but left 7 sessions instead of 3 in CI, where
	// real network latency let the ten goroutines actually overlap.
	//
	// hashtext(user_id) maps the UUID to a stable int4; the advisory
	// lock is released automatically on commit or rollback (the _xact_
	// variant), so there is nothing to unlock explicitly, and it never
	// blocks a *different* user's sessions.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, s.UserID); err != nil {
		return fmt.Errorf("postgres: create session: acquire lock: %w", err)
	}

	const insertQuery = `
		INSERT INTO sessions (token_hash, user_id, expires_at, created_at)
		VALUES ($1, $2::uuid, $3, $4)
	`
	if _, err := tx.ExecContext(ctx, insertQuery, s.TokenHash, s.UserID, s.ExpiresAt, s.CreatedAt); err != nil {
		return fmt.Errorf("postgres: create session: %w", err)
	}

	const evictQuery = `
		DELETE FROM sessions
		WHERE user_id = $1::uuid
		  AND token_hash NOT IN (
		      SELECT token_hash FROM sessions
		      WHERE user_id = $1::uuid
		      ORDER BY created_at DESC
		      LIMIT $2
		  )
	`
	if _, err := tx.ExecContext(ctx, evictQuery, s.UserID, maxSessions); err != nil {
		return fmt.Errorf("postgres: create session: evict oldest: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres: create session: commit transaction: %w", err)
	}
	return nil
}

// DeleteSessionsForUser removes every session belonging to userID.
func (r *postgresRepository) DeleteSessionsForUser(ctx context.Context, userID string) error {
	const query = `DELETE FROM sessions WHERE user_id = $1::uuid`
	if _, err := r.db.ExecContext(ctx, query, userID); err != nil {
		return fmt.Errorf("postgres: delete sessions for user: %w", err)
	}
	return nil
}

// DeleteSessionsForUserExcept removes every session belonging to userID
// other than the one whose hash is keepTokenHash.
func (r *postgresRepository) DeleteSessionsForUserExcept(ctx context.Context, userID, keepTokenHash string) error {
	const query = `DELETE FROM sessions WHERE user_id = $1::uuid AND token_hash != $2`
	if _, err := r.db.ExecContext(ctx, query, userID, keepTokenHash); err != nil {
		return fmt.Errorf("postgres: delete sessions for user except: %w", err)
	}
	return nil
}

// FindSessionByTokenHash returns the session with the given token hash.
// Returns ErrNotFound if absent.
func (r *postgresRepository) FindSessionByTokenHash(ctx context.Context, tokenHash string) (Session, error) {
	const query = `
		SELECT token_hash, user_id::text, expires_at, created_at
		FROM sessions
		WHERE token_hash = $1
	`
	var s Session
	err := r.db.QueryRowContext(ctx, query, tokenHash).
		Scan(&s.TokenHash, &s.UserID, &s.ExpiresAt, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("postgres: find session: %w", err)
	}
	return s, nil
}

// DeleteSession removes the session with the given token hash. Deleting an
// absent session is not an error — see memoryRepository.DeleteSession's
// doc comment for why.
func (r *postgresRepository) DeleteSession(ctx context.Context, tokenHash string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash); err != nil {
		return fmt.Errorf("postgres: delete session: %w", err)
	}
	return nil
}

// DeleteExpiredSessions removes every session whose expires_at is before
// now.
func (r *postgresRepository) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < $1`, now); err != nil {
		return fmt.Errorf("postgres: delete expired sessions: %w", err)
	}
	return nil
}

// userScanner is satisfied by *sql.Row, letting scanUser serve every
// single-row query above without duplicating the column list.
type userScanner interface {
	Scan(dest ...any) error
}

// scanUser reads one row in the fixed column order every query above
// selects: id, email, password_hash, created_at, updated_at.
func scanUser(row userScanner) (User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return User{}, err
	}
	return u, nil
}

// isUniqueViolation reports whether err is a PostgreSQL unique constraint
// violation (SQLSTATE 23505) — the error CreateUser observes when it races
// another writer registering the same email.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == postgreSQLUniqueViolation
}
