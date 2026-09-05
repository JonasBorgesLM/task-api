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
	"fmt"
	"os"
	"sync"
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
	if err := repo.CreateSession(context.Background(), s, unlimitedSessions); err != nil {
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
	if err := repo.CreateSession(context.Background(), s, unlimitedSessions); err != nil {
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
	if err := repo.CreateSession(context.Background(), s, unlimitedSessions); err != nil {
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
		if err := repo.CreateSession(context.Background(), s, unlimitedSessions); err != nil {
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

// --- CreateSession eviction ---

// countSessions counts a user's rows directly via SQL, bypassing
// Repository — used only to verify eviction actually happened, not as a
// method under test.
func countSessions(t *testing.T, db *sql.DB, userID string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM sessions WHERE user_id = $1::uuid`, userID,
	).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

func TestPostgres_CreateSession_UnderCap_KeepsAll(t *testing.T) {
	repo := newPostgresTestRepo(t)
	u := newPostgresTestUser(t, "under-cap@example.com")
	if err := repo.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	for i, hash := range []string{"h1", "h2", "h3"} {
		s := Session{TokenHash: hash, UserID: u.ID, ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(time.Duration(i) * time.Second)}
		if err := repo.CreateSession(context.Background(), s, 5); err != nil {
			t.Fatalf("CreateSession(%s) unexpected error: %v", hash, err)
		}
	}

	if got := countSessions(t, repo.db, u.ID); got != 3 {
		t.Errorf("session count = %d, want 3", got)
	}
}

// TestPostgres_CreateSession_OverCap_EvictsOldestFirst is the SQL half of
// the eviction rule: the DELETE ... NOT IN (... ORDER BY created_at DESC
// LIMIT $2) subquery must keep the newest maxSessions and remove the
// rest, verified against a real query plan and real rows, not the
// in-memory map's own bookkeeping.
func TestPostgres_CreateSession_OverCap_EvictsOldestFirst(t *testing.T) {
	repo := newPostgresTestRepo(t)
	u := newPostgresTestUser(t, "over-cap@example.com")
	if err := repo.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	sessions := []Session{
		{TokenHash: "oldest", UserID: u.ID, ExpiresAt: now.Add(time.Hour), CreatedAt: now},
		{TokenHash: "middle", UserID: u.ID, ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(time.Second)},
		{TokenHash: "newest", UserID: u.ID, ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(2 * time.Second)},
	}
	for _, s := range sessions {
		if err := repo.CreateSession(context.Background(), s, 2); err != nil {
			t.Fatalf("CreateSession(%s) unexpected error: %v", s.TokenHash, err)
		}
	}

	if _, err := repo.FindSessionByTokenHash(context.Background(), "oldest"); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindSessionByTokenHash(oldest) error = %v, want ErrNotFound (should have been evicted)", err)
	}
	for _, hash := range []string{"middle", "newest"} {
		if _, err := repo.FindSessionByTokenHash(context.Background(), hash); err != nil {
			t.Errorf("FindSessionByTokenHash(%s) unexpected error: %v (should have survived)", hash, err)
		}
	}
	if got := countSessions(t, repo.db, u.ID); got != 2 {
		t.Errorf("session count = %d, want 2", got)
	}
}

// TestPostgres_ConcurrentCreateSession_NeverExceedsCap is the concurrency
// test the issue asked for, in the same shape as
// TestPostgres_ConcurrentUpdate_LosersGetErrConflict in internal/task:
// real goroutines, a start gate, run under -race. Every writer creates a
// session for the *same* user at the *same* time.
//
// The property under test is not "who wins" (there's no winner/loser
// here — every CreateSession call is expected to succeed) but that the
// final row count for the user never exceeds the cap, regardless of how
// the calls interleave.
//
// This test is the reason postgresRepository.CreateSession takes an
// advisory lock: a transaction around insert+evict alone is not enough
// under READ COMMITTED, PostgreSQL's default isolation — a version
// without the lock passed here locally, repeatedly, and then left 7
// sessions instead of 3 the first time it ran in CI against real network
// latency between goroutines. This test cannot be trusted to catch that
// class of regression by itself on every machine; see
// postgresRepository.CreateSession's doc comment and docs/DECISIONS.md
// for the mechanism and that finding in full.
func TestPostgres_ConcurrentCreateSession_NeverExceedsCap(t *testing.T) {
	repo := newPostgresTestRepo(t)
	u := newPostgresTestUser(t, "concurrent-cap@example.com")
	if err := repo.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	const sessionCap = 3
	const writers = 10

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		otherErrs []error
	)
	start := make(chan struct{})

	for i := range writers {
		wg.Go(func() {
			<-start
			s := Session{
				TokenHash: fmt.Sprintf("writer-%d", i),
				UserID:    u.ID,
				ExpiresAt: time.Now().Add(time.Hour),
				CreatedAt: time.Now(),
			}
			if err := repo.CreateSession(context.Background(), s, sessionCap); err != nil {
				mu.Lock()
				otherErrs = append(otherErrs, err)
				mu.Unlock()
			}
		})
	}

	close(start)
	wg.Wait()

	for _, err := range otherErrs {
		t.Errorf("CreateSession() unexpected error under concurrency: %v", err)
	}

	if got := countSessions(t, repo.db, u.ID); got != sessionCap {
		t.Errorf("session count after %d concurrent CreateSession() calls = %d, want exactly %d", writers, got, sessionCap)
	}
}

// --- DeleteSessionsForUser ---

func TestPostgres_DeleteSessionsForUser_RemovesAll(t *testing.T) {
	repo := newPostgresTestRepo(t)
	u := newPostgresTestUser(t, "logout-all@example.com")
	if err := repo.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, hash := range []string{"a", "b", "c"} {
		s := Session{TokenHash: hash, UserID: u.ID, ExpiresAt: now.Add(time.Hour), CreatedAt: now}
		if err := repo.CreateSession(context.Background(), s, unlimitedSessions); err != nil {
			t.Fatalf("CreateSession(%s) unexpected error: %v", hash, err)
		}
	}

	if err := repo.DeleteSessionsForUser(context.Background(), u.ID); err != nil {
		t.Fatalf("DeleteSessionsForUser() unexpected error: %v", err)
	}

	if got := countSessions(t, repo.db, u.ID); got != 0 {
		t.Errorf("session count after DeleteSessionsForUser() = %d, want 0", got)
	}
}

func TestPostgres_DeleteSessionsForUser_LeavesOtherUsersAlone(t *testing.T) {
	repo := newPostgresTestRepo(t)
	mine := newPostgresTestUser(t, "mine@example.com")
	theirs := newPostgresTestUser(t, "theirs@example.com")
	if err := repo.CreateUser(context.Background(), mine); err != nil {
		t.Fatalf("CreateUser(mine) unexpected error: %v", err)
	}
	if err := repo.CreateUser(context.Background(), theirs); err != nil {
		t.Fatalf("CreateUser(theirs) unexpected error: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := repo.CreateSession(context.Background(), Session{TokenHash: "mine-token", UserID: mine.ID, ExpiresAt: now.Add(time.Hour), CreatedAt: now}, unlimitedSessions); err != nil {
		t.Fatalf("CreateSession(mine) unexpected error: %v", err)
	}
	if err := repo.CreateSession(context.Background(), Session{TokenHash: "their-token", UserID: theirs.ID, ExpiresAt: now.Add(time.Hour), CreatedAt: now}, unlimitedSessions); err != nil {
		t.Fatalf("CreateSession(theirs) unexpected error: %v", err)
	}

	if err := repo.DeleteSessionsForUser(context.Background(), mine.ID); err != nil {
		t.Fatalf("DeleteSessionsForUser() unexpected error: %v", err)
	}

	if got := countSessions(t, repo.db, theirs.ID); got != 1 {
		t.Errorf("the other user's session count = %d, want 1 (must survive)", got)
	}
}

func TestPostgres_UpdateUserPassword(t *testing.T) {
	repo := newPostgresTestRepo(t)
	u := newPostgresTestUser(t, "change-password@example.com")
	if err := repo.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	if err := repo.UpdateUserPassword(context.Background(), u.ID, "a-new-hash"); err != nil {
		t.Fatalf("UpdateUserPassword() unexpected error: %v", err)
	}

	got, err := repo.FindUserByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("FindUserByID() unexpected error: %v", err)
	}
	if got.PasswordHash != "a-new-hash" {
		t.Errorf("PasswordHash after UpdateUserPassword() = %q, want %q", got.PasswordHash, "a-new-hash")
	}
	if !got.UpdatedAt.After(u.UpdatedAt) {
		t.Errorf("UpdatedAt after UpdateUserPassword() = %v, want it advanced past the original %v", got.UpdatedAt, u.UpdatedAt)
	}
}

func TestPostgres_UpdateUserPassword_NotFound(t *testing.T) {
	repo := newPostgresTestRepo(t)

	id, err := newID()
	if err != nil {
		t.Fatalf("generate user id: %v", err)
	}

	err = repo.UpdateUserPassword(context.Background(), id, "a-new-hash")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateUserPassword() on an unknown id error = %v, want ErrNotFound", err)
	}
}

// TestPostgres_DeleteSessionsForUserExcept_KeepsOnlyGivenSession is the
// Service.ChangePassword half TestPostgres_DeleteSessionsForUser_RemovesAll
// deliberately doesn't cover: unlike LogoutAll, changing a password must
// not also sign out the session that made the call.
func TestPostgres_DeleteSessionsForUserExcept_KeepsOnlyGivenSession(t *testing.T) {
	repo := newPostgresTestRepo(t)
	u := newPostgresTestUser(t, "change-password-sessions@example.com")
	if err := repo.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, hash := range []string{"a", "b", "c"} {
		s := Session{TokenHash: hash, UserID: u.ID, ExpiresAt: now.Add(time.Hour), CreatedAt: now}
		if err := repo.CreateSession(context.Background(), s, unlimitedSessions); err != nil {
			t.Fatalf("CreateSession(%s) unexpected error: %v", hash, err)
		}
	}

	if err := repo.DeleteSessionsForUserExcept(context.Background(), u.ID, "b"); err != nil {
		t.Fatalf("DeleteSessionsForUserExcept() unexpected error: %v", err)
	}

	if got := countSessions(t, repo.db, u.ID); got != 1 {
		t.Errorf("session count after DeleteSessionsForUserExcept() = %d, want 1", got)
	}
	if _, err := repo.FindSessionByTokenHash(context.Background(), "b"); err != nil {
		t.Errorf("FindSessionByTokenHash(\"b\") after DeleteSessionsForUserExcept(keep=\"b\") error = %v, want nil (the kept session must survive)", err)
	}
	for _, hash := range []string{"a", "c"} {
		if _, err := repo.FindSessionByTokenHash(context.Background(), hash); !errors.Is(err, ErrNotFound) {
			t.Errorf("FindSessionByTokenHash(%q) after DeleteSessionsForUserExcept(keep=\"b\") error = %v, want ErrNotFound", hash, err)
		}
	}
}

func TestPostgres_DeleteSessionsForUserExcept_LeavesOtherUsersAlone(t *testing.T) {
	repo := newPostgresTestRepo(t)
	mine := newPostgresTestUser(t, "mine-except@example.com")
	theirs := newPostgresTestUser(t, "theirs-except@example.com")
	if err := repo.CreateUser(context.Background(), mine); err != nil {
		t.Fatalf("CreateUser(mine) unexpected error: %v", err)
	}
	if err := repo.CreateUser(context.Background(), theirs); err != nil {
		t.Fatalf("CreateUser(theirs) unexpected error: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := repo.CreateSession(context.Background(), Session{TokenHash: "mine-token", UserID: mine.ID, ExpiresAt: now.Add(time.Hour), CreatedAt: now}, unlimitedSessions); err != nil {
		t.Fatalf("CreateSession(mine) unexpected error: %v", err)
	}
	if err := repo.CreateSession(context.Background(), Session{TokenHash: "their-token", UserID: theirs.ID, ExpiresAt: now.Add(time.Hour), CreatedAt: now}, unlimitedSessions); err != nil {
		t.Fatalf("CreateSession(theirs) unexpected error: %v", err)
	}

	// keepTokenHash matches nothing of mine's — every one of mine's
	// sessions should still go, and theirs must be untouched regardless.
	if err := repo.DeleteSessionsForUserExcept(context.Background(), mine.ID, "not-a-real-hash"); err != nil {
		t.Fatalf("DeleteSessionsForUserExcept() unexpected error: %v", err)
	}

	if got := countSessions(t, repo.db, mine.ID); got != 0 {
		t.Errorf("mine's session count = %d, want 0", got)
	}
	if got := countSessions(t, repo.db, theirs.ID); got != 1 {
		t.Errorf("the other user's session count = %d, want 1 (must survive)", got)
	}
}
