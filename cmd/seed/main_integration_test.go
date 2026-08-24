//go:build integration

package main

// Integration tests for run() (the logic behind `make seed`/`seed-reset`/
// `db-reset`), run against a real PostgreSQL instance — same build-tag/skip
// safeguards as internal/task's and internal/user's postgres tests; see
// either's doc comment for the full rationale. Pure-validation behavior
// (negative flags) is covered by main_test.go instead, since it needs no
// database at all.

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/JonasBorgesLM/task-api/internal/platform/migrate"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const testDatabaseURLEnv = "TEST_DATABASE_URL"

// setUpTestDB connects to the database named by TEST_DATABASE_URL, points
// run()'s own config.Load() at it via DATABASE_URL (restored automatically
// by t.Setenv after the test), applies migrations, and empties every table
// seeding touches so each test starts from a known-empty state. If
// TEST_DATABASE_URL is unset, the calling test is skipped rather than
// failed.
func setUpTestDB(t *testing.T) *sql.DB {
	t.Helper()

	url := os.Getenv(testDatabaseURLEnv)
	if url == "" {
		t.Skipf("%s not set; skipping PostgreSQL integration test (see docker-compose.yml)", testDatabaseURLEnv)
	}
	t.Setenv("DATABASE_URL", url)

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

	return db
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestPostgres_Seed_SeedsUsersAndTasks(t *testing.T) {
	db := setUpTestDB(t)

	if err := run(seedOptions{users: 2, password: "password12345", tasksPerUser: 3}); err != nil {
		t.Fatalf("run() unexpected error: %v", err)
	}

	if got := countRows(t, db, "users"); got != 2 {
		t.Errorf("users = %d, want 2", got)
	}
	if got := countRows(t, db, "tasks"); got != 6 {
		t.Errorf("tasks = %d, want 6 (2 users * 3 tasks-per-user)", got)
	}

	var distinctOwners int
	if err := db.QueryRowContext(context.Background(), `SELECT count(DISTINCT user_id) FROM tasks`).Scan(&distinctOwners); err != nil {
		t.Fatalf("count distinct task owners: %v", err)
	}
	if distinctOwners != 2 {
		t.Errorf("tasks have %d distinct owners, want 2 (every seeded task must belong to one of the seeded users)", distinctOwners)
	}
}

// TestPostgres_Seed_ReusesExistingUsersAcrossRuns verifies `make seed`'s
// safe-to-rerun contract: running it twice with the same -users/-password
// must not fail on the second run's duplicate emails, and must add tasks
// for the same accounts rather than creating new ones.
func TestPostgres_Seed_ReusesExistingUsersAcrossRuns(t *testing.T) {
	db := setUpTestDB(t)

	if err := run(seedOptions{users: 2, password: "password12345", tasksPerUser: 3}); err != nil {
		t.Fatalf("first run() unexpected error: %v", err)
	}
	if err := run(seedOptions{users: 2, password: "password12345", tasksPerUser: 3}); err != nil {
		t.Fatalf("second run() unexpected error: %v", err)
	}

	if got := countRows(t, db, "users"); got != 2 {
		t.Errorf("users = %d after two runs, want 2 (second run must reuse existing accounts, not duplicate)", got)
	}
	if got := countRows(t, db, "tasks"); got != 12 {
		t.Errorf("tasks = %d after two runs, want 12 (6 from each run)", got)
	}
}

// TestPostgres_Seed_PasswordMismatchOnReuse_ReturnsError verifies that
// re-running with a different -password than a prior run used fails with
// a clear error (via the Authenticate fallback) instead of silently
// succeeding or panicking.
func TestPostgres_Seed_PasswordMismatchOnReuse_ReturnsError(t *testing.T) {
	setUpTestDB(t)

	if err := run(seedOptions{users: 1, password: "password12345", tasksPerUser: 1}); err != nil {
		t.Fatalf("first run() unexpected error: %v", err)
	}

	err := run(seedOptions{users: 1, password: "a-completely-different-password", tasksPerUser: 1})
	if err == nil {
		t.Fatal("run() with a mismatched -password on an already-seeded account: expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "different password") {
		t.Errorf("run() error = %q, want it to explain the password mismatch", err.Error())
	}
}

func TestPostgres_Seed_ResetWipesAllTables(t *testing.T) {
	db := setUpTestDB(t)

	if err := run(seedOptions{users: 2, password: "password12345", tasksPerUser: 3}); err != nil {
		t.Fatalf("run() unexpected error: %v", err)
	}
	if err := run(seedOptions{users: 0, reset: true}); err != nil {
		t.Fatalf("run() with -reset -users=0: unexpected error: %v", err)
	}

	for _, table := range []string{"users", "sessions", "tasks"} {
		if got := countRows(t, db, table); got != 0 {
			t.Errorf("table %s has %d rows after reset, want 0", table, got)
		}
	}
}

// TestPostgres_Seed_UsersZero_IsNoOp verifies that -users=0 without
// -reset seeds nothing and errors on nothing — the shape `make db-reset`
// relies on when combined with -reset, and also a sensible standalone
// no-op.
func TestPostgres_Seed_UsersZero_IsNoOp(t *testing.T) {
	db := setUpTestDB(t)

	if err := run(seedOptions{users: 0}); err != nil {
		t.Fatalf("run() with -users=0: unexpected error: %v", err)
	}

	if got := countRows(t, db, "users"); got != 0 {
		t.Errorf("users = %d after -users=0, want 0", got)
	}
}
