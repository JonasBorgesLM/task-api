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
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const testDatabaseURLEnv = "TEST_DATABASE_URL"

// dropEverySchemaTable empties the schema by enumerating it, rather than
// by naming the tables it expects to find.
//
// It used to be a hardcoded `DROP TABLE IF EXISTS sessions, tasks, users,
// schema_migrations CASCADE`, and that list rots the moment a migration
// adds a table: the bookkeeping table gets dropped, the new table does
// not, and the next RunMigrations fails with `relation "attachments"
// already exists` — a message that points at the new migration rather
// than at the stale teardown that actually caused it, and that only
// reproduces on a database a previous run has already touched. Asking
// the catalogue what is there cannot fall out of sync with what the
// migrations create.
const dropEverySchemaTable = `
DO $$
DECLARE
	r RECORD;
BEGIN
	FOR r IN SELECT tablename FROM pg_tables WHERE schemaname = current_schema() LOOP
		EXECUTE 'DROP TABLE IF EXISTS ' || quote_ident(r.tablename) || ' CASCADE';
	END LOOP;
END $$;
`

// newTestDB connects to the database named by TEST_DATABASE_URL and drops
// every table in the schema (including the bookkeeping table itself), so
// each test starts RunMigrations from a genuinely empty schema regardless
// of what a previous run left behind — unlike internal/task's and
// internal/user's test helpers, which only TRUNCATE (they assume the
// schema already exists; these tests are what create it). If
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
	if _, err := db.ExecContext(ctx, dropEverySchemaTable); err != nil {
		t.Fatalf("drop tables: %v", err)
	}

	return db
}

// assertNewestMigrationIs fails if the newest embedded migration is not
// the one named, so a test that steps back from the tail says plainly
// that it needs a new step rather than failing on whatever the new
// migration happened not to revert.
func assertNewestMigrationIs(t *testing.T, want string) {
	t.Helper()

	paths, err := fs.Glob(migrationFiles, "migrations/*.up.sql")
	if err != nil {
		t.Fatalf("list embedded migrations: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no embedded migrations found")
	}
	sort.Strings(paths)

	newest := strings.TrimSuffix(path.Base(paths[len(paths)-1]), ".up.sql")
	if newest != want {
		t.Fatalf("newest migration is %q, but this test steps back from %q.\n"+
			"A migration was added without extending this test: add a step asserting what %q actually changed, "+
			"then shift the assertions below by one.", newest, want, newest)
	}
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

// indexExists reports whether an index of the given name exists on table.
func indexExists(t *testing.T, db *sql.DB, table, index string) bool {
	t.Helper()
	var exists bool
	err := db.QueryRowContext(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE schemaname = 'public' AND tablename = $1 AND indexname = $2
		)
	`, table, index).Scan(&exists)
	if err != nil {
		t.Fatalf("check index %s on %q exists: %v", index, table, err)
	}
	return exists
}

// embeddedMigrationCount is the number of *.up.sql files this package
// embeds, derived from migrationFiles rather than hardcoded.
//
// The invariant these tests care about is "every embedded migration gets
// applied and recorded" — deriving the number states exactly that. A
// hardcoded constant would instead make every test in this file fail
// whenever a migration is added, for a reason unrelated to what any of
// them actually verify, and with a message ("has 6 rows, want 5") that
// describes the bookkeeping rather than the problem. Assertions that
// genuinely depend on a *specific* migration's contents stay explicit
// about which one they mean — see the step-by-step revert test below.
func embeddedMigrationCount(t *testing.T) int {
	t.Helper()

	paths, err := fs.Glob(migrationFiles, "migrations/*.up.sql")
	if err != nil {
		t.Fatalf("list embedded migrations: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no embedded migrations found — the //go:embed pattern is broken")
	}
	return len(paths)
}

func TestPostgres_RunMigrations_AppliesAllAndIsIdempotent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	wantMigrationCount := embeddedMigrationCount(t)

	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations() unexpected error: %v", err)
	}

	for _, table := range []string{"users", "sessions", "tasks", "attachments"} {
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

// TestPostgres_RunMigrationsDown_RevertsMostRecentMigration verifies that
// each RunMigrationsDown call undoes exactly one migration's own schema
// change — not just that it returns nil — by stepping back through the
// most recent ones and checking what each specifically did:
//
//	0007_create_attachments_table        -> the attachments table is gone
//	0006_add_sessions_indexes            -> its two indexes are dropped
//	0005_expand_task_status_and_priority -> priority column and the widened
//	                                        status CHECK are undone
//
// Checking what a *specific* migration did is what gives this test its
// value over the round-trip test below, and it is also what couples it to
// the tail of the migration list: adding a migration means adding a step
// here. assertNewestMigrationIs makes that failure say so directly,
// instead of letting the mismatch surface as a confusing cascade of
// assertions about indexes and columns that were never reverted.
func TestPostgres_RunMigrationsDown_RevertsMostRecentMigration(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	wantMigrationCount := embeddedMigrationCount(t)

	assertNewestMigrationIs(t, "0007_create_attachments_table")

	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations() unexpected error: %v", err)
	}

	// --- Revert 0007_create_attachments_table ---
	if !tableExists(t, db, "attachments") {
		t.Fatal("attachments table does not exist after RunMigrations — test setup is wrong")
	}

	if err := RunMigrationsDown(ctx, db); err != nil {
		t.Fatalf("RunMigrationsDown() unexpected error: %v", err)
	}
	if tableExists(t, db, "attachments") {
		t.Error("attachments table still exists after reverting 0007_create_attachments_table")
	}

	// --- Revert 0006_add_sessions_indexes ---
	for _, index := range []string{"idx_sessions_expires_at", "idx_sessions_user_id"} {
		if !indexExists(t, db, "sessions", index) {
			t.Fatalf("index %s does not exist after RunMigrations — test setup is wrong", index)
		}
	}

	if err := RunMigrationsDown(ctx, db); err != nil {
		t.Fatalf("RunMigrationsDown() unexpected error: %v", err)
	}
	if got := appliedMigrationCount(t, db); got != wantMigrationCount-2 {
		t.Errorf("schema_migrations has %d rows after two RunMigrationsDown, want %d", got, wantMigrationCount-2)
	}
	for _, index := range []string{"idx_sessions_expires_at", "idx_sessions_user_id"} {
		if indexExists(t, db, "sessions", index) {
			t.Errorf("index %s still exists after reverting 0006_add_sessions_indexes", index)
		}
	}

	// --- Revert 0005_expand_task_status_and_priority ---
	if err := RunMigrationsDown(ctx, db); err != nil {
		t.Fatalf("RunMigrationsDown() (third) unexpected error: %v", err)
	}

	if got := appliedMigrationCount(t, db); got != wantMigrationCount-3 {
		t.Errorf("schema_migrations has %d rows after three RunMigrationsDown, want %d", got, wantMigrationCount-3)
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
	wantMigrationCount := embeddedMigrationCount(t)

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

	for _, table := range []string{"users", "sessions", "tasks", "attachments"} {
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
