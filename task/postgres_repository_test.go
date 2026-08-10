//go:build integration

package task

// Integration tests for postgresRepository, run against a real PostgreSQL
// instance. The "integration" build tag above is what keeps these
// completely separate from the unit test suite (memory_repository_test.go
// and friends): a plain `go test ./...` never even compiles this file, so
// it can never accidentally run — or accidentally get skipped and reported
// as "passing" — as part of the normal unit test run. They only compile
// and run when explicitly requested with `-tags=integration`.
//
// As a second, independent safeguard, newPostgresTestRepo also skips at
// runtime if TEST_DATABASE_URL isn't set — so even a deliberate
// `-tags=integration` run degrades to a clean skip instead of a hard
// failure on a machine with no PostgreSQL available.
//
// To run them locally:
//
//	docker compose up -d postgres          # or: make db-up
//	make test-integration
//
// which is equivalent to:
//
//	TEST_DATABASE_URL="postgres://task_api:task_api@localhost:5432/task_api?sslmode=disable" \
//	    go test -tags=integration ./task/... -run Postgres -v
//
// CI runs these against a postgres service container — see
// .github/workflows/ci.yml.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// testDatabaseURLEnv names the environment variable these tests read to
// find a PostgreSQL instance. It deliberately does not reuse
// config.Config's DATABASE_URL name, so a developer's local .env
// DATABASE_URL (possibly pointing at a database with real data) is never
// accidentally picked up by tests that TRUNCATE the tasks table.
const testDatabaseURLEnv = "TEST_DATABASE_URL"

// newPostgresTestRepo connects to the database named by TEST_DATABASE_URL,
// applies RunMigrations, empties the tasks table, and returns a ready
// postgresRepository along with the underlying *sql.DB (needed by a couple
// of tests that reach past the Repository interface to exercise the
// schema directly). If TEST_DATABASE_URL is unset, the calling test is
// skipped rather than failed.
func newPostgresTestRepo(t *testing.T) (*postgresRepository, *sql.DB) {
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
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	// Every test starts from an empty table; tests never rely on data left
	// behind by another test (or a previous, possibly-crashed run).
	if _, err := db.ExecContext(ctx, `TRUNCATE TABLE tasks`); err != nil {
		t.Fatalf("truncate tasks table: %v", err)
	}

	return &postgresRepository{db: db}, db
}

// newPostgresTestTask returns a Task with a real, randomly generated UUID
// — required because the id column is UUID, so the fixed short IDs
// memory_repository_test.go uses ("1", "2", ...) would fail the ::uuid
// cast in every query.
func newPostgresTestTask(t *testing.T, title string) Task {
	t.Helper()

	id, err := newID()
	if err != nil {
		t.Fatalf("generate task id: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	return Task{
		ID:          id,
		Title:       title,
		Description: "test description",
		Status:      StatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func TestPostgres_CreateAndFindByID(t *testing.T) {
	repo, _ := newPostgresTestRepo(t)
	task := newPostgresTestTask(t, "Buy groceries")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	got, err := repo.FindByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}

	if got.ID != task.ID || got.Title != task.Title || got.Description != task.Description {
		t.Errorf("FindByID() = %+v, want %+v", got, task)
	}
	if got.Status != StatusPending {
		t.Errorf("FindByID() Status = %q, want %q", got.Status, StatusPending)
	}
	if !got.CreatedAt.Equal(task.CreatedAt) {
		t.Errorf("FindByID() CreatedAt = %v, want %v", got.CreatedAt, task.CreatedAt)
	}
	if got.Version != 1 {
		t.Errorf("FindByID() Version = %d, want 1", got.Version)
	}
}

func TestPostgres_FindByID_NotFound(t *testing.T) {
	repo, _ := newPostgresTestRepo(t)

	id, err := newID()
	if err != nil {
		t.Fatalf("generate task id: %v", err)
	}

	_, err = repo.FindByID(context.Background(), id)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByID() error = %v, want ErrNotFound", err)
	}
}

// TestPostgres_FindByID_MalformedID verifies that an ID which is not a
// syntactically valid UUID is rejected by the ::uuid cast rather than
// causing a broken or injected query — the database itself validates the
// shape of the value before it ever compares it against stored rows.
func TestPostgres_FindByID_MalformedID(t *testing.T) {
	repo, _ := newPostgresTestRepo(t)

	_, err := repo.FindByID(context.Background(), "not-a-uuid")
	if err == nil {
		t.Fatal("FindByID() with a malformed ID: expected an error, got nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("FindByID() with a malformed ID: got ErrNotFound, want a query/type error")
	}
}

func TestPostgres_FindAll_OrderedByCreatedAtThenID(t *testing.T) {
	repo, _ := newPostgresTestRepo(t)

	base := time.Now().UTC().Truncate(time.Microsecond)
	tasks := []Task{
		newPostgresTestTask(t, "Third"),
		newPostgresTestTask(t, "First"),
		newPostgresTestTask(t, "Second"),
	}
	// Deliberately out of chronological order relative to creation calls,
	// to prove FindAll's ORDER BY (not insertion order) determines result
	// order.
	tasks[0].CreatedAt, tasks[0].UpdatedAt = base.Add(2*time.Second), base.Add(2*time.Second)
	tasks[1].CreatedAt, tasks[1].UpdatedAt = base, base
	tasks[2].CreatedAt, tasks[2].UpdatedAt = base.Add(1*time.Second), base.Add(1*time.Second)

	for _, task := range tasks {
		if err := repo.Create(context.Background(), task); err != nil {
			t.Fatalf("Create() unexpected error: %v", err)
		}
	}

	got, err := repo.FindAll(context.Background())
	if err != nil {
		t.Fatalf("FindAll() unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("FindAll() returned %d tasks, want 3", len(got))
	}

	wantTitles := []string{"First", "Second", "Third"}
	for i, want := range wantTitles {
		if got[i].Title != want {
			t.Errorf("FindAll()[%d].Title = %q, want %q", i, got[i].Title, want)
		}
	}
}

func TestPostgres_FindAll_Empty(t *testing.T) {
	repo, _ := newPostgresTestRepo(t)

	got, err := repo.FindAll(context.Background())
	if err != nil {
		t.Fatalf("FindAll() unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("FindAll() returned %d tasks, want 0", len(got))
	}
}

func TestPostgres_Update(t *testing.T) {
	repo, _ := newPostgresTestRepo(t)
	task := newPostgresTestTask(t, "Original title")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	current, err := repo.FindByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}

	current.Title = "Updated title"
	current.Status = StatusDone
	current.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)

	if err := repo.Update(context.Background(), current); err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}

	got, err := repo.FindByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}
	if got.Title != "Updated title" {
		t.Errorf("Update() title = %q, want %q", got.Title, "Updated title")
	}
	if got.Status != StatusDone {
		t.Errorf("Update() status = %q, want %q", got.Status, StatusDone)
	}
	if got.Version != 2 {
		t.Errorf("Update() Version = %d, want 2", got.Version)
	}
	// CreatedAt must never change on Update.
	if !got.CreatedAt.Equal(task.CreatedAt) {
		t.Errorf("Update() changed CreatedAt: got %v, want %v", got.CreatedAt, task.CreatedAt)
	}
}

func TestPostgres_Update_NotFound(t *testing.T) {
	repo, _ := newPostgresTestRepo(t)
	task := newPostgresTestTask(t, "Ghost task")

	err := repo.Update(context.Background(), task)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Update() error = %v, want ErrNotFound", err)
	}
}

// TestPostgres_Update_VersionMismatch_ReturnsErrConflict mirrors
// memory_repository_test.go's equivalent test, confirming the
// transactional SELECT ... FOR UPDATE + compare + write sequence enforces
// the same optimistic-concurrency contract the in-memory implementation
// does.
func TestPostgres_Update_VersionMismatch_ReturnsErrConflict(t *testing.T) {
	repo, _ := newPostgresTestRepo(t)
	task := newPostgresTestTask(t, "Original title")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	stale, err := repo.FindByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}

	winner := stale
	winner.Title = "Winner"
	if err := repo.Update(context.Background(), winner); err != nil {
		t.Fatalf("Update() (winner) unexpected error: %v", err)
	}

	stale.Title = "Loser"
	err = repo.Update(context.Background(), stale)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("Update() (stale version) error = %v, want ErrConflict", err)
	}

	got, err := repo.FindByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}
	if got.Title != "Winner" {
		t.Errorf("Update() with stale Version must not overwrite: title = %q, want %q", got.Title, "Winner")
	}
}

// TestPostgres_ConcurrentUpdate_LosersGetErrConflict drives real concurrent
// goroutines, each in its own connection/transaction, through the same
// "lost update" scenario memory_repository_test.go's
// TestConcurrentUpdate_LosersGetErrConflict exercises against the
// in-memory store. Here it also verifies that Update's `SELECT ... FOR
// UPDATE` row lock does its job against real concurrent Postgres
// transactions: exactly one writer must win, everyone else must observe
// ErrConflict, and the table must never end up in a state that mixes two
// writers' data.
func TestPostgres_ConcurrentUpdate_LosersGetErrConflict(t *testing.T) {
	repo, _ := newPostgresTestRepo(t)
	svc := NewService(repo)

	created, err := svc.CreateTask(context.Background(), "Original", "")
	if err != nil {
		t.Fatalf("CreateTask() unexpected error: %v", err)
	}

	const writers = 10

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		successes int
		conflicts int
		otherErrs []error
	)

	start := make(chan struct{})

	for i := range writers {
		wg.Go(func() {
			<-start
			_, err := svc.UpdateTask(context.Background(), created.ID, fmt.Sprintf("Writer %d", i), "")

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrConflict):
				conflicts++
			default:
				otherErrs = append(otherErrs, err)
			}
		})
	}

	close(start)
	wg.Wait()

	for _, err := range otherErrs {
		t.Errorf("UpdateTask() unexpected non-conflict error: %v", err)
	}
	if successes == 0 {
		t.Error("expected at least one writer to succeed, got 0")
	}
	if successes+conflicts != writers {
		t.Errorf("successes(%d) + conflicts(%d) = %d, want %d", successes, conflicts, successes+conflicts, writers)
	}

	final, err := svc.GetTask(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetTask() unexpected error: %v", err)
	}
	if !strings.HasPrefix(final.Title, "Writer ") {
		t.Errorf("final Title = %q, want one of the writers' titles", final.Title)
	}
}

func TestPostgres_Create_AlwaysStartsAtVersionOne(t *testing.T) {
	repo, _ := newPostgresTestRepo(t)
	task := newPostgresTestTask(t, "Title")
	task.Version = 99 // must be ignored by Create

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	got, err := repo.FindByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}
	if got.Version != 1 {
		t.Errorf("Create() Version = %d, want 1", got.Version)
	}
}

func TestPostgres_Create_DuplicateID(t *testing.T) {
	repo, _ := newPostgresTestRepo(t)
	task := newPostgresTestTask(t, "First")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error on first create: %v", err)
	}

	duplicate := task
	duplicate.Title = "Duplicate"
	err := repo.Create(context.Background(), duplicate)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("Create() duplicate error = %v, want ErrAlreadyExists", err)
	}

	got, err := repo.FindByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}
	if got.Title != "First" {
		t.Errorf("Create() duplicate overwrote original: got title %q, want %q", got.Title, "First")
	}
}

// TestPostgres_Create_RejectsSQLInjectionAttempt demonstrates that
// attacker-controlled text lands in the database as inert data, not as
// executed SQL: a title crafted to look like an injection attempt is
// stored (and read back) verbatim, and the attempted DROP TABLE never
// runs — proving the parameterized query ($2 for title) is doing its job
// rather than string-concatenated SQL.
func TestPostgres_Create_RejectsSQLInjectionAttempt(t *testing.T) {
	repo, db := newPostgresTestRepo(t)

	const malicious = `'; DROP TABLE tasks; --`
	task := newPostgresTestTask(t, malicious)

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	got, err := repo.FindByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}
	if got.Title != malicious {
		t.Errorf("Title = %q, want it stored verbatim as %q", got.Title, malicious)
	}

	// The table must still exist and still be queryable.
	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM tasks`).Scan(&count); err != nil {
		t.Fatalf("tasks table appears to have been dropped: %v", err)
	}
	if count != 1 {
		t.Errorf("SELECT count(*) FROM tasks = %d, want 1", count)
	}
}

func TestPostgres_Delete(t *testing.T) {
	repo, _ := newPostgresTestRepo(t)
	task := newPostgresTestTask(t, "To be deleted")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if err := repo.Delete(context.Background(), task.ID); err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}

	_, err := repo.FindByID(context.Background(), task.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByID() after Delete() error = %v, want ErrNotFound", err)
	}
}

func TestPostgres_Delete_NotFound(t *testing.T) {
	repo, _ := newPostgresTestRepo(t)

	id, err := newID()
	if err != nil {
		t.Fatalf("generate task id: %v", err)
	}

	err = repo.Delete(context.Background(), id)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete() error = %v, want ErrNotFound", err)
	}
}

// TestPostgres_ContextCanceled_AbortsQuery verifies that an
// already-canceled context.Context aborts the query instead of running it
// to completion — proof that ctx is actually threaded down into the
// *sql.DB call (QueryRowContext), not just accepted and ignored.
func TestPostgres_ContextCanceled_AbortsQuery(t *testing.T) {
	repo, _ := newPostgresTestRepo(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	id, err := newID()
	if err != nil {
		t.Fatalf("generate task id: %v", err)
	}

	_, err = repo.FindByID(ctx, id)
	if err == nil {
		t.Fatal("FindByID() with a canceled context: expected an error, got nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("FindByID() with a canceled context: got ErrNotFound, want a context-cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("FindByID() with a canceled context: error = %v, want it to wrap context.Canceled", err)
	}
}

// TestPostgres_Schema_RejectsInvalidStatus exercises the tasks_status_check
// CHECK constraint directly via raw SQL — something the Repository's own
// API can never produce, since task.Status only ever takes the values
// StatusPending/StatusDone in Go. It confirms Status is a genuinely
// controlled state at the storage layer too, not just by convention in
// application code.
func TestPostgres_Schema_RejectsInvalidStatus(t *testing.T) {
	_, db := newPostgresTestRepo(t)

	id, err := newID()
	if err != nil {
		t.Fatalf("generate task id: %v", err)
	}

	_, err = db.ExecContext(context.Background(), `
		INSERT INTO tasks (id, title, description, status, created_at, updated_at, version)
		VALUES ($1::uuid, 'Title', '', 'not-a-real-status', now(), now(), 1)
	`, id)
	if err == nil {
		t.Fatal("INSERT with an invalid status: expected a CHECK constraint violation, got nil error")
	}
}
