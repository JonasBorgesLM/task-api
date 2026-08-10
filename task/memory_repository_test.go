package task

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestTask is a helper that returns a Task with predictable values.
func newTestTask(id, title string) Task {
	now := time.Now()
	return Task{
		ID:          id,
		Title:       title,
		Description: "test description",
		Status:      StatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// TestCreateAndFindByID verifies that a created task can be retrieved by ID.
func TestCreateAndFindByID(t *testing.T) {
	repo := NewMemoryRepository()
	task := newTestTask("1", "Buy groceries")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	got, err := repo.FindByID(context.Background(), "1")
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}

	if got.ID != task.ID || got.Title != task.Title {
		t.Errorf("FindByID() = %+v, want %+v", got, task)
	}
}

// TestFindByID_NotFound verifies that ErrNotFound is returned for a missing ID.
func TestFindByID_NotFound(t *testing.T) {
	repo := NewMemoryRepository()

	_, err := repo.FindByID(context.Background(), "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByID() error = %v, want ErrNotFound", err)
	}
}

// TestFindAll verifies that all created tasks are returned.
func TestFindAll(t *testing.T) {
	repo := NewMemoryRepository()

	tasks := []Task{
		newTestTask("1", "Task one"),
		newTestTask("2", "Task two"),
		newTestTask("3", "Task three"),
	}

	for _, task := range tasks {
		if err := repo.Create(context.Background(), task); err != nil {
			t.Fatalf("Create() unexpected error: %v", err)
		}
	}

	got, err := repo.FindAll(context.Background())
	if err != nil {
		t.Fatalf("FindAll() unexpected error: %v", err)
	}

	if len(got) != len(tasks) {
		t.Errorf("FindAll() returned %d tasks, want %d", len(got), len(tasks))
	}
}

// TestUpdate verifies that an existing task is correctly replaced when the
// caller passes back the Version it most recently read.
func TestUpdate(t *testing.T) {
	repo := NewMemoryRepository()
	task := newTestTask("1", "Original title")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	current, err := repo.FindByID(context.Background(), "1")
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}

	current.Title = "Updated title"
	current.Status = StatusDone

	if err := repo.Update(context.Background(), current); err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}

	got, err := repo.FindByID(context.Background(), "1")
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}

	if got.Title != "Updated title" {
		t.Errorf("Update() title = %q, want %q", got.Title, "Updated title")
	}

	if got.Status != StatusDone {
		t.Errorf("Update() status = %q, want %q", got.Status, StatusDone)
	}
}

// TestUpdate_VersionMismatch_ReturnsErrConflict verifies that Update rejects
// a write whose Version no longer matches the stored task — i.e. someone
// else updated the task in between the caller's read and this write.
func TestUpdate_VersionMismatch_ReturnsErrConflict(t *testing.T) {
	repo := NewMemoryRepository()
	task := newTestTask("1", "Original title")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	stale, err := repo.FindByID(context.Background(), "1")
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}

	// A first writer updates successfully, bumping the stored Version.
	winner := stale
	winner.Title = "Winner"
	if err := repo.Update(context.Background(), winner); err != nil {
		t.Fatalf("Update() (winner) unexpected error: %v", err)
	}

	// A second writer, still holding the pre-update Version, must be
	// rejected rather than silently overwriting the winner's change.
	stale.Title = "Loser"
	err = repo.Update(context.Background(), stale)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("Update() (stale version) error = %v, want ErrConflict", err)
	}

	got, err := repo.FindByID(context.Background(), "1")
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}
	if got.Title != "Winner" {
		t.Errorf("Update() with stale Version must not overwrite: title = %q, want %q", got.Title, "Winner")
	}
}

// TestCreate_AlwaysStartsAtVersionOne verifies that Create ignores any
// caller-supplied Version and always starts a new task at Version 1.
func TestCreate_AlwaysStartsAtVersionOne(t *testing.T) {
	repo := NewMemoryRepository()
	task := newTestTask("1", "Title")
	task.Version = 99 // must be ignored by Create

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	got, err := repo.FindByID(context.Background(), "1")
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}
	if got.Version != 1 {
		t.Errorf("Create() Version = %d, want 1", got.Version)
	}
}

// TestUpdate_NotFound verifies that updating a missing task returns ErrNotFound.
func TestUpdate_NotFound(t *testing.T) {
	repo := NewMemoryRepository()
	task := newTestTask("nonexistent", "Ghost task")

	err := repo.Update(context.Background(), task)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Update() error = %v, want ErrNotFound", err)
	}
}

// TestDelete verifies that a task is removed and no longer retrievable.
func TestDelete(t *testing.T) {
	repo := NewMemoryRepository()
	task := newTestTask("1", "To be deleted")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if err := repo.Delete(context.Background(), "1"); err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}

	_, err := repo.FindByID(context.Background(), "1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByID() after Delete() error = %v, want ErrNotFound", err)
	}
}

// TestDelete_NotFound verifies that deleting a missing task returns ErrNotFound.
func TestDelete_NotFound(t *testing.T) {
	repo := NewMemoryRepository()

	err := repo.Delete(context.Background(), "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete() error = %v, want ErrNotFound", err)
	}
}

// TestCreate_DuplicateID verifies that creating a task with an existing ID returns an error.
func TestCreate_DuplicateID(t *testing.T) {
	repo := NewMemoryRepository()
	task := newTestTask("1", "First")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error on first create: %v", err)
	}

	duplicate := newTestTask("1", "Duplicate")
	err := repo.Create(context.Background(), duplicate)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("Create() duplicate error = %v, want ErrAlreadyExists", err)
	}

	// Confirm the original task was not overwritten.
	got, _ := repo.FindByID(context.Background(), "1")
	if got.Title != "First" {
		t.Errorf("Create() duplicate overwrote original: got title %q, want %q", got.Title, "First")
	}
}

// TestFindByID_IsolatesInternalState verifies that modifying the returned Task
// does not affect the value stored in the repository.
func TestFindByID_IsolatesInternalState(t *testing.T) {
	repo := NewMemoryRepository()
	task := newTestTask("1", "Original")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	got, _ := repo.FindByID(context.Background(), "1")
	got.Title = "Mutated"
	got.Status = StatusDone

	stored, _ := repo.FindByID(context.Background(), "1")
	if stored.Title != "Original" {
		t.Errorf("FindByID() isolation failed: stored title = %q, want %q", stored.Title, "Original")
	}

	if stored.Status != StatusPending {
		t.Errorf("FindByID() isolation failed: stored status = %q, want %q", stored.Status, StatusPending)
	}
}

// TestFindAll_IsolatesInternalState verifies that modifying tasks returned by FindAll
// does not affect the values stored in the repository.
func TestFindAll_IsolatesInternalState(t *testing.T) {
	repo := NewMemoryRepository()
	task := newTestTask("1", "Original")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	all, _ := repo.FindAll(context.Background())
	all[0].Title = "Mutated"
	all[0].Status = StatusDone

	stored, _ := repo.FindByID(context.Background(), "1")
	if stored.Title != "Original" {
		t.Errorf("FindAll() isolation failed: stored title = %q, want %q", stored.Title, "Original")
	}

	if stored.Status != StatusPending {
		t.Errorf("FindAll() isolation failed: stored status = %q, want %q", stored.Status, StatusPending)
	}
}

// TestConcurrentAccess verifies that concurrent Creates are all persisted correctly.
// It creates 50 tasks concurrently, asserts no Create returns an error, and then
// confirms FindAll returns exactly 51 tasks (50 concurrent + 1 seed).
func TestConcurrentAccess(t *testing.T) {
	repo := NewMemoryRepository()

	// Pre-populate with a known task.
	seed := newTestTask("seed", "Seed task")
	if err := repo.Create(context.Background(), seed); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	const goroutines = 50

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for i := range goroutines {
		wg.Go(func() {
			task := newTestTask(fmt.Sprintf("task-%d", i), "concurrent task")
			if err := repo.Create(context.Background(), task); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}

	wg.Wait()

	// No Create should have failed.
	for _, err := range errs {
		t.Errorf("Create() unexpected error: %v", err)
	}

	// All 50 concurrent tasks plus the seed must be present.
	all, err := repo.FindAll(context.Background())
	if err != nil {
		t.Fatalf("FindAll() unexpected error: %v", err)
	}

	const want = goroutines + 1 // 50 concurrent + 1 seed
	if len(all) != want {
		t.Errorf("FindAll() returned %d tasks, want %d", len(all), want)
	}
}

// TestConcurrentUpdate_LosersGetErrConflict drives real concurrent
// goroutines through the "lost update" scenario the optimistic-concurrency
// fix targets: many callers read the same task and then all try to write
// it back. Before the fix (no Version/CAS), every write would succeed and
// whichever finished last would silently discard every other caller's
// change. Now, only a writer whose read is still current can succeed —
// everyone else must observe ErrConflict instead of a silent lost update.
//
// This goes through Service (not just Repository directly) so it exercises
// the exact read-then-write pattern UpdateTask/CompleteTask use in
// production; run with -race to also confirm no data race.
func TestConcurrentUpdate_LosersGetErrConflict(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo)

	created, err := svc.CreateTask(context.Background(), "Original", "")
	if err != nil {
		t.Fatalf("CreateTask() unexpected error: %v", err)
	}

	const writers = 20

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
			<-start // release all goroutines together to maximize overlap
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

	// Whichever writer's update actually landed, it must be internally
	// consistent — not a corrupted mix of two writers' data.
	final, err := svc.GetTask(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetTask() unexpected error: %v", err)
	}
	if !strings.HasPrefix(final.Title, "Writer ") {
		t.Errorf("final Title = %q, want one of the writers' titles", final.Title)
	}
}
