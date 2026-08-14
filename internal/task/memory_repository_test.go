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

// testUserID and otherUserID are the two owners used throughout these
// tests: testUserID for the "normal" case, otherUserID to verify
// ownership scoping actually isolates one user's tasks from another's.
const (
	testUserID  = "owner-1"
	otherUserID = "owner-2"
)

// newTestTask is a helper that returns a Task owned by testUserID with
// predictable values.
func newTestTask(id, title string) Task {
	now := time.Now()
	return Task{
		ID:          id,
		UserID:      testUserID,
		Title:       title,
		Description: "test description",
		Status:      StatusPending,
		Priority:    PriorityMedium,
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

	got, err := repo.FindByID(context.Background(), "1", testUserID)
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

	_, err := repo.FindByID(context.Background(), "nonexistent", testUserID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByID() error = %v, want ErrNotFound", err)
	}
}

// TestFindByID_WrongUser_ReturnsErrNotFound verifies that a task owned by
// a different user is reported as ErrNotFound, not a distinct "forbidden"
// error — see Repository's doc comment on why.
func TestFindByID_WrongUser_ReturnsErrNotFound(t *testing.T) {
	repo := NewMemoryRepository()
	task := newTestTask("1", "Someone else's task")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	_, err := repo.FindByID(context.Background(), "1", otherUserID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByID() by non-owner error = %v, want ErrNotFound", err)
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

	got, err := repo.FindAll(context.Background(), testUserID, -1, 0)
	if err != nil {
		t.Fatalf("FindAll() unexpected error: %v", err)
	}

	if len(got) != len(tasks) {
		t.Errorf("FindAll() returned %d tasks, want %d", len(got), len(tasks))
	}
}

// TestFindAll_ScopedToUser verifies that FindAll never returns another
// user's tasks.
func TestFindAll_ScopedToUser(t *testing.T) {
	repo := NewMemoryRepository()

	mine := newTestTask("1", "Mine")
	theirs := newTestTask("2", "Theirs")
	theirs.UserID = otherUserID

	for _, task := range []Task{mine, theirs} {
		if err := repo.Create(context.Background(), task); err != nil {
			t.Fatalf("Create() unexpected error: %v", err)
		}
	}

	got, err := repo.FindAll(context.Background(), testUserID, -1, 0)
	if err != nil {
		t.Fatalf("FindAll() unexpected error: %v", err)
	}

	if len(got) != 1 || got[0].ID != "1" {
		t.Errorf("FindAll() = %+v, want only task 1", got)
	}
}

// TestFindAll_OrdersByCreatedAtThenID verifies that FindAll itself
// (not a caller re-sorting its result) returns tasks ordered by CreatedAt
// ascending, ties broken by ID — required now that Repository.FindAll's
// contract guarantees this ordering directly, since the store is a Go map
// with no iteration order of its own.
func TestFindAll_OrdersByCreatedAtThenID(t *testing.T) {
	repo := NewMemoryRepository()

	base := time.Now()
	tasks := []Task{
		{ID: "z", UserID: testUserID, CreatedAt: base.Add(time.Minute)},
		{ID: "y", UserID: testUserID, CreatedAt: base.Add(time.Minute)}, // ties with "z"; ID breaks the tie
		{ID: "a", UserID: testUserID, CreatedAt: base},
		{ID: "b", UserID: testUserID, CreatedAt: base.Add(-time.Hour)},
	}
	for _, task := range tasks {
		if err := repo.Create(context.Background(), task); err != nil {
			t.Fatalf("Create() unexpected error: %v", err)
		}
	}

	got, err := repo.FindAll(context.Background(), testUserID, -1, 0)
	if err != nil {
		t.Fatalf("FindAll() unexpected error: %v", err)
	}

	wantOrder := []string{"b", "a", "y", "z"}
	if len(got) != len(wantOrder) {
		t.Fatalf("FindAll() returned %d tasks, want %d", len(got), len(wantOrder))
	}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Errorf("FindAll()[%d].ID = %q, want %q", i, got[i].ID, id)
		}
	}
}

// TestFindAll_Pagination verifies the limit/offset windowing FindAll
// applies over its ordered result.
func TestFindAll_Pagination(t *testing.T) {
	repo := NewMemoryRepository()

	base := time.Now()
	for i, id := range []string{"1", "2", "3", "4", "5"} {
		task := newTestTask(id, "Task "+id)
		task.CreatedAt = base.Add(time.Duration(i) * time.Second)
		if err := repo.Create(context.Background(), task); err != nil {
			t.Fatalf("Create() unexpected error: %v", err)
		}
	}

	cases := []struct {
		name    string
		limit   int
		offset  int
		wantIDs []string
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
			got, err := repo.FindAll(context.Background(), testUserID, tc.limit, tc.offset)
			if err != nil {
				t.Fatalf("FindAll() unexpected error: %v", err)
			}

			gotIDs := make([]string, len(got))
			for i, task := range got {
				gotIDs[i] = task.ID
			}
			if len(gotIDs) != len(tc.wantIDs) {
				t.Fatalf("FindAll() IDs = %v, want %v", gotIDs, tc.wantIDs)
			}
			for i := range tc.wantIDs {
				if gotIDs[i] != tc.wantIDs[i] {
					t.Errorf("FindAll() IDs = %v, want %v", gotIDs, tc.wantIDs)
				}
			}
		})
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

	current, err := repo.FindByID(context.Background(), "1", testUserID)
	if err != nil {
		t.Fatalf("FindByID() unexpected error: %v", err)
	}

	current.Title = "Updated title"
	current.Status = StatusDone

	if err := repo.Update(context.Background(), current); err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}

	got, err := repo.FindByID(context.Background(), "1", testUserID)
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

// TestUpdate_WrongUser_ReturnsErrNotFound verifies that Update rejects a
// task whose UserID doesn't match the stored row's owner, before even
// looking at Version.
func TestUpdate_WrongUser_ReturnsErrNotFound(t *testing.T) {
	repo := NewMemoryRepository()
	task := newTestTask("1", "Original title")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	current, err := repo.FindByID(context.Background(), "1", testUserID)
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

// TestUpdate_VersionMismatch_ReturnsErrConflict verifies that Update rejects
// a write whose Version no longer matches the stored task — i.e. someone
// else updated the task in between the caller's read and this write.
func TestUpdate_VersionMismatch_ReturnsErrConflict(t *testing.T) {
	repo := NewMemoryRepository()
	task := newTestTask("1", "Original title")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	stale, err := repo.FindByID(context.Background(), "1", testUserID)
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

	got, err := repo.FindByID(context.Background(), "1", testUserID)
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

	got, err := repo.FindByID(context.Background(), "1", testUserID)
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

	if err := repo.Delete(context.Background(), "1", testUserID); err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}

	_, err := repo.FindByID(context.Background(), "1", testUserID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByID() after Delete() error = %v, want ErrNotFound", err)
	}
}

// TestDelete_NotFound verifies that deleting a missing task returns ErrNotFound.
func TestDelete_NotFound(t *testing.T) {
	repo := NewMemoryRepository()

	err := repo.Delete(context.Background(), "nonexistent", testUserID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete() error = %v, want ErrNotFound", err)
	}
}

// TestDelete_WrongUser_ReturnsErrNotFound verifies that Delete refuses to
// remove a task owned by a different user.
func TestDelete_WrongUser_ReturnsErrNotFound(t *testing.T) {
	repo := NewMemoryRepository()
	task := newTestTask("1", "Not yours")

	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	err := repo.Delete(context.Background(), "1", otherUserID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete() by non-owner error = %v, want ErrNotFound", err)
	}

	if _, err := repo.FindByID(context.Background(), "1", testUserID); err != nil {
		t.Errorf("task should still exist after a non-owner's failed Delete: %v", err)
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
	got, _ := repo.FindByID(context.Background(), "1", testUserID)
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

	got, _ := repo.FindByID(context.Background(), "1", testUserID)
	got.Title = "Mutated"
	got.Status = StatusDone

	stored, _ := repo.FindByID(context.Background(), "1", testUserID)
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

	all, _ := repo.FindAll(context.Background(), testUserID, -1, 0)
	all[0].Title = "Mutated"
	all[0].Status = StatusDone

	stored, _ := repo.FindByID(context.Background(), "1", testUserID)
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
	all, err := repo.FindAll(context.Background(), testUserID, -1, 0)
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
// production; run with -race to also confirm no data race. Every goroutine
// uses the same userID throughout — this is testing the Version race, not
// ownership.
func TestConcurrentUpdate_LosersGetErrConflict(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo)

	created, err := svc.CreateTask(context.Background(), testUserID, "Original", "", "")
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
			_, err := svc.UpdateTask(context.Background(), testUserID, created.ID, fmt.Sprintf("Writer %d", i), "", "")

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
	final, err := svc.GetTask(context.Background(), testUserID, created.ID)
	if err != nil {
		t.Fatalf("GetTask() unexpected error: %v", err)
	}
	if !strings.HasPrefix(final.Title, "Writer ") {
		t.Errorf("final Title = %q, want one of the writers' titles", final.Title)
	}
}
