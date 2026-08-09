package task

import (
	"context"
	"errors"
	"fmt"
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

// TestUpdate verifies that an existing task is correctly replaced.
func TestUpdate(t *testing.T) {
	repo := NewMemoryRepository()
	task := newTestTask("1", "Original title")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	task.Title = "Updated title"
	task.Status = StatusDone

	if err := repo.Update(context.Background(), task); err != nil {
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

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			task := newTestTask(fmt.Sprintf("task-%d", i), "concurrent task")
			if err := repo.Create(context.Background(), task); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
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
