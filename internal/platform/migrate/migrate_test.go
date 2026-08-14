//go:build integration

package migrate

// Integration tests for RunMigrations/RunMigrationsDown, run against a real
// PostgreSQL instance — same build-tag/skip safeguards as
// internal/task/postgres_repository_test.go and
// internal/user/postgres_repository_test.go; see either's doc comment for
// the full rationale.
//
// These specifically exist to exercise RunMigrationsDown and every
// .down.sql file: before this file, `make migrate-down` was a code path
// with zero test coverage — a real risk for a command whose entire
// purpose is to be a safety valve someone reaches for under pressure.

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const testDatabaseURLEnv = "TEST_DATABASE_URL"

// newTestDB connects to the database named by TEST_DATABASE_URL and drops
// every table these migrations touch (plus the bookkeeping table itself),
// so each test starts RunMigrations from a genuinely empty schema
// regardless of what a previous run left behind — unlike
// internal/task/internal/user's test helpers, which only TRUNCATE (they
// assume the schema already exists; these tests are what create it). If
// TEST_DATABASE_URL is unset, the calling test is skipped rather than
// failed.
func newTestDB(t *testing.T) *sql.DB {
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
	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS sessions, tasks, users, schema_migrations CASCADE`); err != nil {
		t.Fatalf("drop tables: %v", err)
	}

	return db
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var exists bool
	err := db.QueryRowContext(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`, name,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("check table %q exists: %v", name, err)
	}
	return exists
}

func columnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var exists bool
	err := db.QueryRowContext(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = $1 AND column_name = $2
		)
	`, table, column).Scan(&exists)
	if err != nil {
		t.Fatalf("check column %s.%s exists: %v", table, column, err)
	}
	return exists
}

func appliedMigrationCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	return n
}

// wantMigrationCount is the number of migration files under migrations/ —
// kept as one named constant so every assertion below states its
// expectation the same way; if a migration is added, every one of these
// tests fails loudly with a mismatched count instead of silently checking
// a stale number.
const wantMigrationCount = 5

func TestPostgres_RunMigrations_AppliesAllAndIsIdempotent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations() unexpected error: %v", err)
	}

	for _, table := range []string{"users", "sessions", "tasks"} {
		if !tableExists(t, db, table) {
			t.Errorf("table %q does not exist after RunMigrations", table)
		}
	}
	if got := appliedMigrationCount(t, db); got != wantMigrationCount {
		t.Errorf("schema_migrations has %d rows, want %d", got, wantMigrationCount)
	}

	// Safe to call on every process startup: running again must not error
	// or duplicate records — see RunMigrations' doc comment.
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations() second call unexpected error: %v", err)
	}
	if got := appliedMigrationCount(t, db); got != wantMigrationCount {
		t.Errorf("schema_migrations has %d rows after second RunMigrations, want %d", got, wantMigrationCount)
	}
}

func TestPostgres_RunMigrationsDown_NoMigrationsToRevert(t *testing.T) {
	db := newTestDB(t)

	// ensureMigrationsTable runs as part of RunMigrationsDown itself, so
	// this exercises the "table exists but empty" case, not "table
	// doesn't exist yet" — both should behave identically from the
	// caller's perspective (ErrNoMigrationsToRevert), which is exactly
	// what makes ensureMigrationsTable safe to call unconditionally here.
	err := RunMigrationsDown(context.Background(), db)
	if !errors.Is(err, ErrNoMigrationsToRevert) {
		t.Errorf("RunMigrationsDown() on empty schema error = %v, want ErrNoMigrationsToRevert", err)
	}
}

// TestPostgres_RunMigrationsDown_RevertsMostRecentMigration verifies reverting the
// single most recently applied migration (0005) actually undoes its
// specific schema change — not just that RunMigrationsDown returns nil.
func TestPostgres_RunMigrationsDown_RevertsMostRecentMigration(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations() unexpected error: %v", err)
	}
	if err := RunMigrationsDown(ctx, db); err != nil {
		t.Fatalf("RunMigrationsDown() unexpected error: %v", err)
	}

	if got := appliedMigrationCount(t, db); got != wantMigrationCount-1 {
		t.Errorf("schema_migrations has %d rows after one RunMigrationsDown, want %d", got, wantMigrationCount-1)
	}
	if columnExists(t, db, "tasks", "priority") {
		t.Error("priority column still exists after reverting 0005_expand_task_status_and_priority")
	}

	// The restored (pre-0005) CHECK constraint must reject the status
	// values 0005 added — proving the down migration didn't just drop the
	// priority column but genuinely restored the original constraint,
	// not merely leave a wider one in place.
	const userID = "11111111-1111-1111-1111-111111111111"
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1::uuid, 'revert-test@example.com', 'x')`, userID); err != nil {
		t.Fatalf("insert test user: %v", err)
	}

	_, err := db.ExecContext(ctx, `
		INSERT INTO tasks (id, user_id, title, description, status, created_at, updated_at, version)
		VALUES ('22222222-2222-2222-2222-222222222222'::uuid, $1::uuid, 'T', '', 'in_progress', now(), now(), 1)
	`, userID)
	if err == nil {
		t.Error("INSERT with status='in_progress' after reverting 0005: expected a CHECK constraint violation, got nil error")
	}
}

// TestPostgres_RunMigrationsDown_FullRoundTrip reverts every applied migration, one
// at a time, until none remain — proving the entire down chain (not just
// the most recent step) is valid SQL that fully unwinds the schema — and
// then re-applies everything, proving the reverted state is genuinely
// clean rather than leaving residue (e.g. a leftover index/constraint
// under a name an up migration expects to create fresh).
func TestPostgres_RunMigrationsDown_FullRoundTrip(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations() unexpected error: %v", err)
	}

	reverted := 0
	for {
		err := RunMigrationsDown(ctx, db)
		if errors.Is(err, ErrNoMigrationsToRevert) {
			break
		}
		if err != nil {
			t.Fatalf("RunMigrationsDown() after %d reverts: unexpected error: %v", reverted, err)
		}
		reverted++
		if reverted > wantMigrationCount {
			t.Fatalf("RunMigrationsDown() did not converge to ErrNoMigrationsToRevert after %d reverts — possible infinite loop", reverted)
		}
	}
	if reverted != wantMigrationCount {
		t.Errorf("RunMigrationsDown() reverted %d migrations, want %d", reverted, wantMigrationCount)
	}

	for _, table := range []string{"users", "sessions", "tasks"} {
		if tableExists(t, db, table) {
			t.Errorf("table %q still exists after fully reverting every migration", table)
		}
	}
	if got := appliedMigrationCount(t, db); got != 0 {
		t.Errorf("schema_migrations has %d rows after fully reverting, want 0", got)
	}

	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations() after full revert: unexpected error: %v", err)
	}
	if got := appliedMigrationCount(t, db); got != wantMigrationCount {
		t.Errorf("schema_migrations has %d rows after re-applying from a fully-reverted state, want %d", got, wantMigrationCount)
	}
}
