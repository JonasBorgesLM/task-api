package task

import (
	"errors"
	"testing"
	"time"
)

// fakeRepository is a test double for Repository.
// Each field controls the behavior of the corresponding method.
type fakeRepository struct {
	// Controlled responses.
	findByIDTask Task
	findByIDErr  error
	findAllTasks []Task
	findAllErr   error
	createErr    error
	updateErr    error
	deleteErr    error

	// Call recording.
	updateCalled bool
	savedTask    Task
	updatedTask  Task
}

func (f *fakeRepository) Create(task Task) error {
	f.savedTask = task
	return f.createErr
}

func (f *fakeRepository) FindByID(_ string) (Task, error) {
	return f.findByIDTask, f.findByIDErr
}

func (f *fakeRepository) FindAll() ([]Task, error) {
	return f.findAllTasks, f.findAllErr
}

func (f *fakeRepository) Update(task Task) error {
	f.updateCalled = true
	f.updatedTask = task
	return f.updateErr
}

func (f *fakeRepository) Delete(_ string) error {
	return f.deleteErr
}

// newFakeTask returns a Task with predictable values for use in tests.
func newFakeTask(status Status) Task {
	t := time.Now().Add(-time.Hour) // one hour ago, to ensure UpdatedAt changes are detectable
	return Task{
		ID:          "fake-id",
		Title:       "Fake task",
		Description: "Fake description",
		Status:      status,
		CreatedAt:   t,
		UpdatedAt:   t,
	}
}

// --- CreateTask ---

func TestCreateTask_ValidTitle(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo)

	got, err := svc.CreateTask("Buy groceries", "at the market")
	if err != nil {
		t.Fatalf("CreateTask() unexpected error: %v", err)
	}

	if got.ID == "" {
		t.Error("CreateTask() ID must not be empty")
	}
	if got.Title != "Buy groceries" {
		t.Errorf("CreateTask() Title = %q, want %q", got.Title, "Buy groceries")
	}
	if got.Description != "at the market" {
		t.Errorf("CreateTask() Description = %q, want %q", got.Description, "at the market")
	}
	if got.Status != StatusPending {
		t.Errorf("CreateTask() Status = %q, want %q", got.Status, StatusPending)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreateTask() CreatedAt must not be zero")
	}
	if got.UpdatedAt.IsZero() {
		t.Error("CreateTask() UpdatedAt must not be zero")
	}
	if !got.CreatedAt.Equal(got.UpdatedAt) {
		t.Errorf("CreateTask() CreatedAt (%v) must equal UpdatedAt (%v)", got.CreatedAt, got.UpdatedAt)
	}

	// Verify the Task sent to the Repository matches the returned Task exactly.
	if repo.savedTask.ID != got.ID {
		t.Errorf("CreateTask() savedTask.ID = %q, want %q", repo.savedTask.ID, got.ID)
	}
	if repo.savedTask.Title != got.Title {
		t.Errorf("CreateTask() savedTask.Title = %q, want %q", repo.savedTask.Title, got.Title)
	}
	if repo.savedTask.Description != got.Description {
		t.Errorf("CreateTask() savedTask.Description = %q, want %q", repo.savedTask.Description, got.Description)
	}
	if repo.savedTask.Status != got.Status {
		t.Errorf("CreateTask() savedTask.Status = %q, want %q", repo.savedTask.Status, got.Status)
	}
	if !repo.savedTask.CreatedAt.Equal(got.CreatedAt) {
		t.Errorf("CreateTask() savedTask.CreatedAt = %v, want %v", repo.savedTask.CreatedAt, got.CreatedAt)
	}
}

func TestCreateTask_EmptyTitle(t *testing.T) {
	svc := NewService(&fakeRepository{})

	_, err := svc.CreateTask("", "description")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("CreateTask() empty title error = %v, want ErrInvalidInput", err)
	}
}

func TestCreateTask_WhitespaceTitle(t *testing.T) {
	svc := NewService(&fakeRepository{})

	_, err := svc.CreateTask("   ", "description")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("CreateTask() whitespace title error = %v, want ErrInvalidInput", err)
	}
}

func TestCreateTask_RepositoryError(t *testing.T) {
	repoErr := errors.New("storage failure")
	svc := NewService(&fakeRepository{createErr: repoErr})

	_, err := svc.CreateTask("Valid title", "")
	if !errors.Is(err, repoErr) {
		t.Errorf("CreateTask() repository error = %v, want %v", err, repoErr)
	}
}

// --- GetTask ---

func TestGetTask_Delegates(t *testing.T) {
	task := newFakeTask(StatusPending)
	svc := NewService(&fakeRepository{findByIDTask: task})

	got, err := svc.GetTask("fake-id")
	if err != nil {
		t.Fatalf("GetTask() unexpected error: %v", err)
	}
	if got.ID != task.ID {
		t.Errorf("GetTask() ID = %q, want %q", got.ID, task.ID)
	}
}

func TestGetTask_NotFound(t *testing.T) {
	svc := NewService(&fakeRepository{findByIDErr: ErrNotFound})

	_, err := svc.GetTask("nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetTask() error = %v, want ErrNotFound", err)
	}
}

// --- ListTasks ---

func TestListTasks_Delegates(t *testing.T) {
	tasks := []Task{newFakeTask(StatusPending), newFakeTask(StatusDone)}
	svc := NewService(&fakeRepository{findAllTasks: tasks})

	got, err := svc.ListTasks()
	if err != nil {
		t.Fatalf("ListTasks() unexpected error: %v", err)
	}
	if len(got) != len(tasks) {
		t.Errorf("ListTasks() returned %d tasks, want %d", len(got), len(tasks))
	}
}

// --- UpdateTask ---

func TestUpdateTask_ValidTitle(t *testing.T) {
	original := newFakeTask(StatusPending)
	repo := &fakeRepository{findByIDTask: original}
	svc := NewService(repo)

	got, err := svc.UpdateTask("fake-id", "New title", "New description")
	if err != nil {
		t.Fatalf("UpdateTask() unexpected error: %v", err)
	}

	if got.Title != "New title" {
		t.Errorf("UpdateTask() Title = %q, want %q", got.Title, "New title")
	}
	if got.Description != "New description" {
		t.Errorf("UpdateTask() Description = %q, want %q", got.Description, "New description")
	}
	if got.ID != original.ID {
		t.Errorf("UpdateTask() ID changed: got %q, want %q", got.ID, original.ID)
	}
	if !got.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("UpdateTask() CreatedAt changed: got %v, want %v", got.CreatedAt, original.CreatedAt)
	}
	if !got.UpdatedAt.After(original.UpdatedAt) {
		t.Errorf("UpdateTask() UpdatedAt (%v) must be after original (%v)", got.UpdatedAt, original.UpdatedAt)
	}
	if !repo.updateCalled {
		t.Error("UpdateTask() must call Repository.Update")
	}
}

func TestUpdateTask_EmptyTitle(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo)

	_, err := svc.UpdateTask("fake-id", "", "desc")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("UpdateTask() empty title error = %v, want ErrInvalidInput", err)
	}
	if repo.updateCalled {
		t.Error("UpdateTask() must not call Repository.Update on invalid input")
	}
}

func TestUpdateTask_WhitespaceTitle(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo)

	_, err := svc.UpdateTask("fake-id", "   ", "desc")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("UpdateTask() whitespace title error = %v, want ErrInvalidInput", err)
	}
	if repo.updateCalled {
		t.Error("UpdateTask() must not call Repository.Update on invalid input")
	}
}

func TestUpdateTask_NotFound(t *testing.T) {
	svc := NewService(&fakeRepository{findByIDErr: ErrNotFound})

	_, err := svc.UpdateTask("nonexistent", "Title", "")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateTask() error = %v, want ErrNotFound", err)
	}
}

func TestUpdateTask_RepositoryUpdateError(t *testing.T) {
	repoErr := errors.New("update failure")
	repo := &fakeRepository{
		findByIDTask: newFakeTask(StatusPending),
		updateErr:    repoErr,
	}
	svc := NewService(repo)

	_, err := svc.UpdateTask("fake-id", "Title", "")
	if !errors.Is(err, repoErr) {
		t.Errorf("UpdateTask() repository error = %v, want %v", err, repoErr)
	}
}

// --- DeleteTask ---

func TestDeleteTask_Delegates(t *testing.T) {
	svc := NewService(&fakeRepository{})

	if err := svc.DeleteTask("fake-id"); err != nil {
		t.Errorf("DeleteTask() unexpected error: %v", err)
	}
}

func TestDeleteTask_NotFound(t *testing.T) {
	svc := NewService(&fakeRepository{deleteErr: ErrNotFound})

	err := svc.DeleteTask("nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteTask() error = %v, want ErrNotFound", err)
	}
}

func TestDeleteTask_RepositoryError(t *testing.T) {
	repoErr := errors.New("delete failure")
	svc := NewService(&fakeRepository{deleteErr: repoErr})

	err := svc.DeleteTask("fake-id")
	if !errors.Is(err, repoErr) {
		t.Errorf("DeleteTask() repository error = %v, want %v", err, repoErr)
	}
}

// --- CompleteTask ---

func TestCompleteTask_PendingToDone(t *testing.T) {
	original := newFakeTask(StatusPending)
	repo := &fakeRepository{findByIDTask: original}
	svc := NewService(repo)

	got, err := svc.CompleteTask("fake-id")
	if err != nil {
		t.Fatalf("CompleteTask() unexpected error: %v", err)
	}
	if got.Status != StatusDone {
		t.Errorf("CompleteTask() Status = %q, want %q", got.Status, StatusDone)
	}
	if !got.UpdatedAt.After(original.UpdatedAt) {
		t.Errorf("CompleteTask() UpdatedAt (%v) must be after original (%v)", got.UpdatedAt, original.UpdatedAt)
	}
	if !repo.updateCalled {
		t.Error("CompleteTask() must call Repository.Update")
	}
}

func TestCompleteTask_AlreadyDone_Idempotent(t *testing.T) {
	original := newFakeTask(StatusDone)
	repo := &fakeRepository{findByIDTask: original}
	svc := NewService(repo)

	got, err := svc.CompleteTask("fake-id")
	if err != nil {
		t.Fatalf("CompleteTask() unexpected error: %v", err)
	}
	if got.Status != StatusDone {
		t.Errorf("CompleteTask() Status = %q, want %q", got.Status, StatusDone)
	}
	if !got.UpdatedAt.Equal(original.UpdatedAt) {
		t.Errorf("CompleteTask() idempotent: UpdatedAt changed from %v to %v", original.UpdatedAt, got.UpdatedAt)
	}
	if repo.updateCalled {
		t.Error("CompleteTask() must NOT call Repository.Update when task is already Done")
	}
}

func TestCompleteTask_NotFound(t *testing.T) {
	svc := NewService(&fakeRepository{findByIDErr: ErrNotFound})

	_, err := svc.CompleteTask("nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("CompleteTask() error = %v, want ErrNotFound", err)
	}
}

func TestCompleteTask_RepositoryUpdateError(t *testing.T) {
	repoErr := errors.New("update failure")
	repo := &fakeRepository{
		findByIDTask: newFakeTask(StatusPending),
		updateErr:    repoErr,
	}
	svc := NewService(repo)

	_, err := svc.CompleteTask("fake-id")
	if !errors.Is(err, repoErr) {
		t.Errorf("CompleteTask() repository error = %v, want %v", err, repoErr)
	}
}
