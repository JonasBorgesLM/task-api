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
//	    go test -tags=integration ./internal/... -run Postgres -v
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

	"github.com/JonasBorgesLM/task-api/internal/platform/migrate"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// testDatabaseURLEnv names the environment variable these tests read to
// find a PostgreSQL instance. It deliberately does not reuse
// config.Config's DATABASE_URL name, so a developer's local .env
// DATABASE_URL (possibly pointing at a database with real data) is never
// accidentally picked up by tests that TRUNCATE the tasks table.
const testDatabaseURLEnv = "TEST_DATABASE_URL"

// newPostgresTestRepo connects to the database named by TEST_DATABASE_URL,
// applies RunMigrations, empties every table these tests touch, creates
// one test user (tasks.user_id is a foreign key — every task needs an
// owner that actually exists), and returns a ready postgresRepository,
// the underlying *sql.DB (needed by a couple of tests that reach past the
// Repository interface to exercise the schema directly), and that user's
// ID. If TEST_DATABASE_URL is unset, the calling test is skipped rather
// than failed.
func newPostgresTestRepo(t *testing.T) (repo *postgresRepository, db *sql.DB, userID string) {
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
	// Every test starts from empty tables; tests never rely on data left
	// behind by another test (or a previous, possibly-crashed run).
	if _, err := db.ExecContext(ctx, `TRUNCATE TABLE sessions, tasks, users CASCADE`); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}

	// Goes through the exported constructor (rather than a bare struct
	// literal) so these tests also exercise NewPostgresRepository itself,
	// not just the type it returns.
	return NewPostgresRepository(db).(*postgresRepository), db, createTestUser(t, db)
}

// createTestUser inserts a minimal user row directly via SQL — this file
// tests task's postgresRepository, not user's, so it deliberately doesn't
// import the user package or go through user.Service just to satisfy
// tasks.user_id's foreign key.
func createTestUser(t *testing.T, db *sql.DB) string {
	t.Helper()

	id, err := newID()
	if err != nil {
		t.Fatalf("generate user id: %v", err)
	}

	_, err = db.ExecContext(context.Background(), `
		INSERT INTO users (id, email, password_hash) VALUES ($1::uuid, $2, 'not-a-real-hash')
	`, id, id+"@example.com")
	if err != nil {
		t.Fatalf("insert test user: %v", err)
	}

	return id
}

// newPostgresTestTask returns a Task owned by userID with a real, randomly
// generated UUID — required because the id column is UUID, so the fixed
// short IDs memory_repository_test.go uses ("1", "2", ...) would fail the
// ::uuid cast in every query.
func newPostgresTestTask(t *testing.T, userID, title string) Task {
	t.Helper()

	id, err := newID()
	if err != nil {
		t.Fatalf("generate task id: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	return Task{
		ID:          id,
		UserID:      userID,
		Title:       title,
		Description: "test description",
		Status:      StatusPending,
		Priority:    PriorityMedium,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func TestPostgres_CreateAndFindByID(t *testing.T) {
	repo, _, userID := newPostgresTestRepo(t)
	task := newPostgresTestTask(t, userID, "Buy groceries")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	got, err := repo.FindByID(context.Background(), task.ID, userID)
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}

	if got.ID != task.ID || got.Title != task.Title || got.Description != task.Description {
		t.Errorf("FindByID() = %+v, want %+v", got, task)
	}
	if got.Status != StatusPending {
		t.Errorf("FindByID() Status = %q, want %q", got.Status, StatusPending)
	}
	if got.Priority != PriorityMedium {
		t.Errorf("FindByID() Priority = %q, want %q", got.Priority, PriorityMedium)
	}
	if !got.CreatedAt.Equal(task.CreatedAt) {
		t.Errorf("FindByID() CreatedAt = %v, want %v", got.CreatedAt, task.CreatedAt)
	}
	if got.Version != 1 {
		t.Errorf("FindByID() Version = %d, want 1", got.Version)
	}
}

func TestPostgres_FindByID_NotFound(t *testing.T) {
	repo, _, userID := newPostgresTestRepo(t)

	id, err := newID()
	if err != nil {
		t.Fatalf("generate task id: %v", err)
	}

	_, err = repo.FindByID(context.Background(), id, userID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByID() error = %v, want ErrNotFound", err)
	}
}

// TestPostgres_FindByID_WrongUser_ReturnsErrNotFound verifies that a task
// owned by a different user is reported as ErrNotFound — see Repository's
// doc comment on why this is deliberately not a distinct "forbidden"
// error.
func TestPostgres_FindByID_WrongUser_ReturnsErrNotFound(t *testing.T) {
	repo, db, userID := newPostgresTestRepo(t)
	otherUserID := createTestUser(t, db)

	task := newPostgresTestTask(t, userID, "Not yours")
	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	_, err := repo.FindByID(context.Background(), task.ID, otherUserID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByID() by non-owner error = %v, want ErrNotFound", err)
	}
}

// TestPostgres_FindByID_MalformedID verifies that an ID which is not a
// syntactically valid UUID is rejected by the ::uuid cast rather than
// causing a broken or injected query — the database itself validates the
// shape of the value before it ever compares it against stored rows.
func TestPostgres_FindByID_MalformedID(t *testing.T) {
	repo, _, userID := newPostgresTestRepo(t)

	_, err := repo.FindByID(context.Background(), "not-a-uuid", userID)
	if err == nil {
		t.Fatal("FindByID() with a malformed ID: expected an error, got nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("FindByID() with a malformed ID: got ErrNotFound, want a query/type error")
	}
}

func TestPostgres_FindAll_OrderedByCreatedAtThenID(t *testing.T) {
	repo, _, userID := newPostgresTestRepo(t)

	base := time.Now().UTC().Truncate(time.Microsecond)
	tasks := []Task{
		newPostgresTestTask(t, userID, "Third"),
		newPostgresTestTask(t, userID, "First"),
		newPostgresTestTask(t, userID, "Second"),
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

	got, err := repo.FindAll(context.Background(), userID, -1, 0)
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
	repo, _, userID := newPostgresTestRepo(t)

	got, err := repo.FindAll(context.Background(), userID, -1, 0)
	if err != nil {
		t.Fatalf("FindAll() unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("FindAll() returned %d tasks, want 0", len(got))
	}
}

// TestPostgres_FindAll_ScopedToUser verifies that FindAll never returns
// another user's tasks — the WHERE user_id = $1 clause, not just
// application-level filtering.
func TestPostgres_FindAll_ScopedToUser(t *testing.T) {
	repo, db, userID := newPostgresTestRepo(t)
	otherUserID := createTestUser(t, db)

	mine := newPostgresTestTask(t, userID, "Mine")
	theirs := newPostgresTestTask(t, otherUserID, "Theirs")
	for _, task := range []Task{mine, theirs} {
		if err := repo.Create(context.Background(), task); err != nil {
			t.Fatalf("Create() unexpected error: %v", err)
		}
	}

	got, err := repo.FindAll(context.Background(), userID, -1, 0)
	if err != nil {
		t.Fatalf("FindAll() unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != mine.ID {
		t.Errorf("FindAll() = %+v, want only %q", got, mine.ID)
	}
}

// TestPostgres_FindAll_Pagination verifies that limit/offset are applied by
// the SQL query itself (LIMIT/OFFSET, see postgresRepository.FindAll) —
// not by fetching every row and slicing in Go — while still respecting the
// (created_at, id) ordering.
func TestPostgres_FindAll_Pagination(t *testing.T) {
	repo, _, userID := newPostgresTestRepo(t)

	base := time.Now().UTC().Truncate(time.Microsecond)
	titles := []string{"1", "2", "3", "4", "5"}
	for i, title := range titles {
		task := newPostgresTestTask(t, userID, title)
		task.CreatedAt = base.Add(time.Duration(i) * time.Second)
		task.UpdatedAt = task.CreatedAt
		if err := repo.Create(context.Background(), task); err != nil {
			t.Fatalf("Create() unexpected error: %v", err)
		}
	}

	cases := []struct {
		name       string
		limit      int
		offset     int
		wantTitles []string
	}{
		{"no limit, no offset", -1, 0, []string{"1", "2", "3", "4", "5"}},
		{"limit only", 2, 0, []string{"1", "2"}},
		{"offset only", -1, 3, []string{"4", "5"}},
		{"limit and offset", 2, 1, []string{"2", "3"}},
		{"limit beyond end", 100, 0, []string{"1", "2", "3", "4", "5"}},
		{"offset beyond end", -1, 100, []string{}},
		{"limit zero", 0, 0, []string{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repo.FindAll(context.Background(), userID, tc.limit, tc.offset)
			if err != nil {
				t.Fatalf("FindAll() unexpected error: %v", err)
			}

			gotTitles := make([]string, len(got))
			for i, task := range got {
				gotTitles[i] = task.Title
			}
			if len(gotTitles) != len(tc.wantTitles) {
				t.Fatalf("FindAll() titles = %v, want %v", gotTitles, tc.wantTitles)
			}
			for i := range tc.wantTitles {
				if gotTitles[i] != tc.wantTitles[i] {
					t.Errorf("FindAll() titles = %v, want %v", gotTitles, tc.wantTitles)
				}
			}
		})
	}
}

func TestPostgres_Update(t *testing.T) {
	repo, _, userID := newPostgresTestRepo(t)
	task := newPostgresTestTask(t, userID, "Original title")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	current, err := repo.FindByID(context.Background(), task.ID, userID)
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}

	current.Title = "Updated title"
	current.Status = StatusDone
	current.Priority = PriorityHigh
	current.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
	// Actively attempt to move CreatedAt, mirroring
	// TestUpdate_PreservesCreatedAt against the in-memory implementation:
	// both Repositories must refuse to change it, and asserting that here
	// only proves something if the test actually tries.
	current.CreatedAt = task.CreatedAt.Add(-365 * 24 * time.Hour)

	if err := repo.Update(context.Background(), current); err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}

	got, err := repo.FindByID(context.Background(), task.ID, userID)
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}
	if got.Title != "Updated title" {
		t.Errorf("Update() title = %q, want %q", got.Title, "Updated title")
	}
	if got.Status != StatusDone {
		t.Errorf("Update() status = %q, want %q", got.Status, StatusDone)
	}
	if got.Priority != PriorityHigh {
		t.Errorf("Update() priority = %q, want %q", got.Priority, PriorityHigh)
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
	repo, _, userID := newPostgresTestRepo(t)
	task := newPostgresTestTask(t, userID, "Ghost task")

	err := repo.Update(context.Background(), task)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Update() error = %v, want ErrNotFound", err)
	}
}

// TestPostgres_Update_WrongUser_ReturnsErrNotFound verifies that Update's
// locked-row ownership check (see postgresRepository.Update's doc
// comment) rejects a task whose UserID doesn't match the stored row.
func TestPostgres_Update_WrongUser_ReturnsErrNotFound(t *testing.T) {
	repo, db, userID := newPostgresTestRepo(t)
	otherUserID := createTestUser(t, db)

	task := newPostgresTestTask(t, userID, "Original title")
	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	current, err := repo.FindByID(context.Background(), task.ID, userID)
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}
	current.UserID = otherUserID
	current.Title = "Hijacked"

	err = repo.Update(context.Background(), current)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Update() with mismatched UserID error = %v, want ErrNotFound", err)
	}
}

// TestPostgres_Update_VersionMismatch_ReturnsErrConflict mirrors
// memory_repository_test.go's equivalent test, confirming the
// transactional SELECT ... FOR UPDATE + compare + write sequence enforces
// the same optimistic-concurrency contract the in-memory implementation
// does.
func TestPostgres_Update_VersionMismatch_ReturnsErrConflict(t *testing.T) {
	repo, _, userID := newPostgresTestRepo(t)
	task := newPostgresTestTask(t, userID, "Original title")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	stale, err := repo.FindByID(context.Background(), task.ID, userID)
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

	got, err := repo.FindByID(context.Background(), task.ID, userID)
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
	repo, _, userID := newPostgresTestRepo(t)
	svc := NewService(repo)

	created, err := svc.CreateTask(context.Background(), userID, "Original", "", "")
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
			_, err := svc.UpdateTask(context.Background(), userID, created.ID, fmt.Sprintf("Writer %d", i), "", "")

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

	final, err := svc.GetTask(context.Background(), userID, created.ID)
	if err != nil {
		t.Fatalf("GetTask() unexpected error: %v", err)
	}
	if !strings.HasPrefix(final.Title, "Writer ") {
		t.Errorf("final Title = %q, want one of the writers' titles", final.Title)
	}
}

func TestPostgres_Create_AlwaysStartsAtVersionOne(t *testing.T) {
	repo, _, userID := newPostgresTestRepo(t)
	task := newPostgresTestTask(t, userID, "Title")
	task.Version = 99 // must be ignored by Create

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	got, err := repo.FindByID(context.Background(), task.ID, userID)
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}
	if got.Version != 1 {
		t.Errorf("Create() Version = %d, want 1", got.Version)
	}
}

func TestPostgres_Create_DuplicateID(t *testing.T) {
	repo, _, userID := newPostgresTestRepo(t)
	task := newPostgresTestTask(t, userID, "First")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error on first create: %v", err)
	}

	duplicate := task
	duplicate.Title = "Duplicate"
	err := repo.Create(context.Background(), duplicate)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("Create() duplicate error = %v, want ErrAlreadyExists", err)
	}

	got, err := repo.FindByID(context.Background(), task.ID, userID)
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
	repo, db, userID := newPostgresTestRepo(t)

	const malicious = `'; DROP TABLE tasks; --`
	task := newPostgresTestTask(t, userID, malicious)

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	got, err := repo.FindByID(context.Background(), task.ID, userID)
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
	repo, _, userID := newPostgresTestRepo(t)
	task := newPostgresTestTask(t, userID, "To be deleted")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if err := repo.Delete(context.Background(), task.ID, userID); err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}

	_, err := repo.FindByID(context.Background(), task.ID, userID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByID() after Delete() error = %v, want ErrNotFound", err)
	}
}

func TestPostgres_Delete_NotFound(t *testing.T) {
	repo, _, userID := newPostgresTestRepo(t)

	id, err := newID()
	if err != nil {
		t.Fatalf("generate task id: %v", err)
	}

	err = repo.Delete(context.Background(), id, userID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete() error = %v, want ErrNotFound", err)
	}
}

// TestPostgres_Delete_WrongUser_ReturnsErrNotFound verifies that Delete's
// `WHERE id = $1 AND user_id = $2` refuses to remove a task owned by a
// different user.
func TestPostgres_Delete_WrongUser_ReturnsErrNotFound(t *testing.T) {
	repo, db, userID := newPostgresTestRepo(t)
	otherUserID := createTestUser(t, db)

	task := newPostgresTestTask(t, userID, "Not yours")
	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	err := repo.Delete(context.Background(), task.ID, otherUserID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete() by non-owner error = %v, want ErrNotFound", err)
	}

	if _, err := repo.FindByID(context.Background(), task.ID, userID); err != nil {
		t.Errorf("task should still exist after a non-owner's failed Delete: %v", err)
	}
}

// TestPostgres_ContextCanceled_AbortsQuery verifies that an
// already-canceled context.Context aborts the query instead of running it
// to completion — proof that ctx is actually threaded down into the
// *sql.DB call (QueryRowContext), not just accepted and ignored.
func TestPostgres_ContextCanceled_AbortsQuery(t *testing.T) {
	repo, _, userID := newPostgresTestRepo(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	id, err := newID()
	if err != nil {
		t.Fatalf("generate task id: %v", err)
	}

	_, err = repo.FindByID(ctx, id, userID)
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
// API can never produce, since task.Status only ever takes one of its
// four known values in Go. It confirms Status is a genuinely controlled
// state at the storage layer too, not just by convention in application
// code.
func TestPostgres_Schema_RejectsInvalidStatus(t *testing.T) {
	_, db, userID := newPostgresTestRepo(t)

	id, err := newID()
	if err != nil {
		t.Fatalf("generate task id: %v", err)
	}

	_, err = db.ExecContext(context.Background(), `
		INSERT INTO tasks (id, user_id, title, description, status, created_at, updated_at, version)
		VALUES ($1::uuid, $2::uuid, 'Title', '', 'not-a-real-status', now(), now(), 1)
	`, id, userID)
	if err == nil {
		t.Fatal("INSERT with an invalid status: expected a CHECK constraint violation, got nil error")
	}
}

// TestPostgres_Schema_RejectsInvalidPriority mirrors
// TestPostgres_Schema_RejectsInvalidStatus for the priority column's
// CHECK constraint.
func TestPostgres_Schema_RejectsInvalidPriority(t *testing.T) {
	_, db, userID := newPostgresTestRepo(t)

	id, err := newID()
	if err != nil {
		t.Fatalf("generate task id: %v", err)
	}

	_, err = db.ExecContext(context.Background(), `
		INSERT INTO tasks (id, user_id, title, description, status, priority, created_at, updated_at, version)
		VALUES ($1::uuid, $2::uuid, 'Title', '', 'pending', 'not-a-real-priority', now(), now(), 1)
	`, id, userID)
	if err == nil {
		t.Fatal("INSERT with an invalid priority: expected a CHECK constraint violation, got nil error")
	}
}
