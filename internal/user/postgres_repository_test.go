//go:build integration

package user

// Integration tests for user's postgresRepository, run against a real
// PostgreSQL instance — mirrors internal/task/postgres_repository_test.go's
// structure and safeguards (build tag + runtime skip on missing
// TEST_DATABASE_URL). See that file's doc comment for the full rationale;
// not repeated here.

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/JonasBorgesLM/task-api/internal/platform/migrate"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const testDatabaseURLEnv = "TEST_DATABASE_URL"

// newPostgresTestRepo connects to the database named by TEST_DATABASE_URL,
// applies migrations, empties every table these tests touch, and returns
// a ready postgresRepository. If TEST_DATABASE_URL is unset, the calling
// test is skipped rather than failed.
func newPostgresTestRepo(t *testing.T) *postgresRepository {
	t.Helper()

	url := os.Getenv(testDatabaseURLEnv)
	if url == "" {
		t.Skipf("%s not set; skipping PostgreSQL integration test (see docker-compose.yml)", testDatabaseURLEnv)
	}

	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping database: %v", err)
	}
	if err := migrate.RunMigrations(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if _, err := db.ExecContext(ctx, `TRUNCATE TABLE sessions, tasks, users CASCADE`); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}

	// Goes through the exported constructor (rather than a bare struct
	// literal) so these tests also exercise NewPostgresRepository itself,
	// not just the type it returns.
	return NewPostgresRepository(db).(*postgresRepository)
}

func newPostgresTestUser(t *testing.T, email string) User {
	t.Helper()

	id, err := newID()
	if err != nil {
		t.Fatalf("generate user id: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	return User{
		ID:           id,
		Email:        email,
		PasswordHash: "not-a-real-hash",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func TestPostgres_CreateAndFindUserByEmail(t *testing.T) {
	repo := newPostgresTestRepo(t)
	u := newPostgresTestUser(t, "user@example.com")

	if err := repo.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	got, err := repo.FindUserByEmail(context.Background(), "user@example.com")
	if err != nil {
		t.Fatalf("FindUserByEmail() unexpected error: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("FindUserByEmail() ID = %q, want %q", got.ID, u.ID)
	}
	if got.PasswordHash != u.PasswordHash {
		t.Errorf("FindUserByEmail() PasswordHash = %q, want %q", got.PasswordHash, u.PasswordHash)
	}
}

func TestPostgres_FindUserByEmail_NotFound(t *testing.T) {
	repo := newPostgresTestRepo(t)

	_, err := repo.FindUserByEmail(context.Background(), "nobody@example.com")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindUserByEmail() error = %v, want ErrNotFound", err)
	}
}

func TestPostgres_CreateUser_DuplicateEmail(t *testing.T) {
	repo := newPostgresTestRepo(t)
	u := newPostgresTestUser(t, "user@example.com")

	if err := repo.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	duplicate := newPostgresTestUser(t, "user@example.com")
	err := repo.CreateUser(context.Background(), duplicate)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("CreateUser() duplicate email error = %v, want ErrAlreadyExists", err)
	}
}

func TestPostgres_FindUserByID(t *testing.T) {
	repo := newPostgresTestRepo(t)
	u := newPostgresTestUser(t, "user@example.com")

	if err := repo.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	got, err := repo.FindUserByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("FindUserByID() unexpected error: %v", err)
	}
	if got.Email != u.Email {
		t.Errorf("FindUserByID() Email = %q, want %q", got.Email, u.Email)
	}
}

func TestPostgres_FindUserByID_NotFound(t *testing.T) {
	repo := newPostgresTestRepo(t)

	id, err := newID()
	if err != nil {
		t.Fatalf("generate user id: %v", err)
	}

	_, err = repo.FindUserByID(context.Background(), id)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindUserByID() error = %v, want ErrNotFound", err)
	}
}

func TestPostgres_CreateAndFindSessionByTokenHash(t *testing.T) {
	repo := newPostgresTestRepo(t)
	u := newPostgresTestUser(t, "user@example.com")
	if err := repo.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	s := Session{
		TokenHash: "a-token-hash",
		UserID:    u.ID,
		ExpiresAt: time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond),
		CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
	}
	if err := repo.CreateSession(context.Background(), s); err != nil {
		t.Fatalf("CreateSession() unexpected error: %v", err)
	}

	got, err := repo.FindSessionByTokenHash(context.Background(), "a-token-hash")
	if err != nil {
		t.Fatalf("FindSessionByTokenHash() unexpected error: %v", err)
	}
	if got.UserID != u.ID {
		t.Errorf("FindSessionByTokenHash() UserID = %q, want %q", got.UserID, u.ID)
	}
}

func TestPostgres_FindSessionByTokenHash_NotFound(t *testing.T) {
	repo := newPostgresTestRepo(t)

	_, err := repo.FindSessionByTokenHash(context.Background(), "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindSessionByTokenHash() error = %v, want ErrNotFound", err)
	}
}

// TestPostgres_CreateSession_CascadesOnUserDeletion verifies the
// sessions.user_id foreign key's ON DELETE CASCADE: deleting a user must
// also remove their sessions, so a stale session can never outlive the
// account it authenticates.
func TestPostgres_CreateSession_CascadesOnUserDeletion(t *testing.T) {
	repo := newPostgresTestRepo(t)
	u := newPostgresTestUser(t, "user@example.com")
	if err := repo.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	s := Session{TokenHash: "a-token-hash", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	if err := repo.CreateSession(context.Background(), s); err != nil {
		t.Fatalf("CreateSession() unexpected error: %v", err)
	}

	if _, err := repo.db.ExecContext(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, u.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	_, err := repo.FindSessionByTokenHash(context.Background(), "a-token-hash")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindSessionByTokenHash() after owning user deleted error = %v, want ErrNotFound (CASCADE)", err)
	}
}

func TestPostgres_DeleteSession(t *testing.T) {
	repo := newPostgresTestRepo(t)
	u := newPostgresTestUser(t, "user@example.com")
	if err := repo.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	s := Session{TokenHash: "a-token-hash", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	if err := repo.CreateSession(context.Background(), s); err != nil {
		t.Fatalf("CreateSession() unexpected error: %v", err)
	}

	if err := repo.DeleteSession(context.Background(), "a-token-hash"); err != nil {
		t.Fatalf("DeleteSession() unexpected error: %v", err)
	}

	_, err := repo.FindSessionByTokenHash(context.Background(), "a-token-hash")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindSessionByTokenHash() after DeleteSession() error = %v, want ErrNotFound", err)
	}
}

func TestPostgres_DeleteSession_Unknown_IsNotAnError(t *testing.T) {
	repo := newPostgresTestRepo(t)

	if err := repo.DeleteSession(context.Background(), "nonexistent"); err != nil {
		t.Errorf("DeleteSession() on an unknown token error = %v, want nil", err)
	}
}

// TestPostgres_DeleteExpiredSessions mirrors
// memory_repository_test.go's equivalent test: only a session whose
// expires_at is before the given time is removed.
func TestPostgres_DeleteExpiredSessions(t *testing.T) {
	repo := newPostgresTestRepo(t)
	u := newPostgresTestUser(t, "prune-test@example.com")
	if err := repo.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	expired := Session{TokenHash: "expired", UserID: u.ID, ExpiresAt: now.Add(-time.Hour), CreatedAt: now.Add(-2 * time.Hour)}
	valid := Session{TokenHash: "valid", UserID: u.ID, ExpiresAt: now.Add(time.Hour), CreatedAt: now}

	for _, s := range []Session{expired, valid} {
		if err := repo.CreateSession(context.Background(), s); err != nil {
			t.Fatalf("CreateSession() unexpected error: %v", err)
		}
	}

	if err := repo.DeleteExpiredSessions(context.Background(), now); err != nil {
		t.Fatalf("DeleteExpiredSessions() unexpected error: %v", err)
	}

	if _, err := repo.FindSessionByTokenHash(context.Background(), "expired"); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindSessionByTokenHash(expired) after DeleteExpiredSessions() error = %v, want ErrNotFound", err)
	}
	if _, err := repo.FindSessionByTokenHash(context.Background(), "valid"); err != nil {
		t.Errorf("FindSessionByTokenHash(valid) after DeleteExpiredSessions() unexpected error: %v", err)
	}
}

// TestPostgres_Schema_UniqueConstraintIsCaseSensitive documents that the
// email UNIQUE constraint is case-sensitive at the schema level —
// "User@example.com" and "user@example.com" are accepted as two distinct
// rows, not rejected as duplicates. Whether to normalize case is a
// Service-level decision (there isn't one currently), not something this
// test asserts should change; it exists so a future change to that
// behavior — in either direction — is a deliberate one, not a silent side
// effect of a schema tweak.
func TestPostgres_Schema_UniqueConstraintIsCaseSensitive(t *testing.T) {
	repo := newPostgresTestRepo(t)

	lower := newPostgresTestUser(t, "user@example.com")
	if err := repo.CreateUser(context.Background(), lower); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	upper := newPostgresTestUser(t, "User@example.com")
	if err := repo.CreateUser(context.Background(), upper); err != nil {
		t.Errorf("CreateUser() with different-case email unexpected error: %v", err)
	}
}
