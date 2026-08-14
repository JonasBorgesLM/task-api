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

// CreateSession persists a new session.
func (r *postgresRepository) CreateSession(ctx context.Context, s Session) error {
	const query = `
		INSERT INTO sessions (token_hash, user_id, expires_at, created_at)
		VALUES ($1, $2::uuid, $3, $4)
	`
	_, err := r.db.ExecContext(ctx, query, s.TokenHash, s.UserID, s.ExpiresAt, s.CreatedAt)
	if err != nil {
		return fmt.Errorf("postgres: create session: %w", err)
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
