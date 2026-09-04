package task

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// validTaskID is a syntactically valid UUID used wherever a test needs a
// well-formed task ID for the fakeRepository to answer for. Service now
// rejects a malformed ID before Repository is ever reached (see
// TestGetTask_MalformedID_IsNotFound and its siblings below), so any
// caller-supplied ID in these tests has to look like a real one — the
// same reason isValidID exists in the first place.
const validTaskID = "11111111-1111-1111-1111-111111111111"

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
	findByIDCalled    bool
	updateCalled      bool
	deleteCalled      bool
	savedTask         Task
	updatedTask       Task
	findAllCalledWith [5]any // [userID, limit, offset, status, priority]
}

func (f *fakeRepository) Create(_ context.Context, task Task) error {
	f.savedTask = task
	return f.createErr
}

func (f *fakeRepository) FindByID(_ context.Context, _, _ string) (Task, error) {
	f.findByIDCalled = true
	return f.findByIDTask, f.findByIDErr
}

func (f *fakeRepository) FindAll(_ context.Context, userID string, limit, offset int, status Status, priority Priority) ([]Task, error) {
	f.findAllCalledWith = [5]any{userID, limit, offset, status, priority}
	return f.findAllTasks, f.findAllErr
}

func (f *fakeRepository) Update(_ context.Context, task Task) error {
	f.updateCalled = true
	f.updatedTask = task
	return f.updateErr
}

func (f *fakeRepository) Delete(_ context.Context, _, _ string) error {
	f.deleteCalled = true
	return f.deleteErr
}

// newFakeTask returns a Task owned by testUserID with predictable values
// for use in tests.
func newFakeTask(status Status) Task {
	t := time.Now().Add(-time.Hour) // one hour ago, to ensure UpdatedAt changes are detectable
	return Task{
		ID:          "fake-id",
		UserID:      testUserID,
		Title:       "Fake task",
		Description: "Fake description",
		Status:      status,
		Priority:    PriorityMedium,
		CreatedAt:   t,
		UpdatedAt:   t,
	}
}

// --- CreateTask ---

func TestCreateTask_ValidTitle(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo)

	got, err := svc.CreateTask(context.Background(), testUserID, "Buy groceries", "at the market", "")
	if err != nil {
		t.Fatalf("CreateTask() unexpected error: %v", err)
	}

	if got.ID == "" {
		t.Error("CreateTask() ID must not be empty")
	}
	if got.UserID != testUserID {
		t.Errorf("CreateTask() UserID = %q, want %q", got.UserID, testUserID)
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
	if got.Priority != PriorityMedium {
		t.Errorf("CreateTask() Priority = %q, want %q (default)", got.Priority, PriorityMedium)
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

func TestCreateTask_ExplicitPriority(t *testing.T) {
	svc := NewService(&fakeRepository{})

	got, err := svc.CreateTask(context.Background(), testUserID, "Title", "", "high")
	if err != nil {
		t.Fatalf("CreateTask() unexpected error: %v", err)
	}
	if got.Priority != PriorityHigh {
		t.Errorf("CreateTask() Priority = %q, want %q", got.Priority, PriorityHigh)
	}
}

func TestCreateTask_InvalidPriority(t *testing.T) {
	svc := NewService(&fakeRepository{})

	_, err := svc.CreateTask(context.Background(), testUserID, "Title", "", "urgent")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("CreateTask() invalid priority error = %v, want ErrInvalidInput", err)
	}
}

func TestCreateTask_EmptyTitle(t *testing.T) {
	svc := NewService(&fakeRepository{})

	_, err := svc.CreateTask(context.Background(), testUserID, "", "description", "")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("CreateTask() empty title error = %v, want ErrInvalidInput", err)
	}
}

func TestCreateTask_WhitespaceTitle(t *testing.T) {
	svc := NewService(&fakeRepository{})

	_, err := svc.CreateTask(context.Background(), testUserID, "   ", "description", "")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("CreateTask() whitespace title error = %v, want ErrInvalidInput", err)
	}
}

func TestCreateTask_TitleTooLong(t *testing.T) {
	svc := NewService(&fakeRepository{})

	_, err := svc.CreateTask(context.Background(), testUserID, strings.Repeat("a", maxTitleLen+1), "description", "")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("CreateTask() overlong title error = %v, want ErrInvalidInput", err)
	}
}

func TestCreateTask_TitleAtMaxLength(t *testing.T) {
	svc := NewService(&fakeRepository{})

	_, err := svc.CreateTask(context.Background(), testUserID, strings.Repeat("a", maxTitleLen), "description", "")
	if err != nil {
		t.Errorf("CreateTask() title at max length unexpected error: %v", err)
	}
}

func TestCreateTask_DescriptionTooLong(t *testing.T) {
	svc := NewService(&fakeRepository{})

	_, err := svc.CreateTask(context.Background(), testUserID, "Valid title", strings.Repeat("a", maxDescriptionLen+1), "")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("CreateTask() overlong description error = %v, want ErrInvalidInput", err)
	}
}

// TestCreateTask_TitleUnicode_AtMaxLength verifies that length is measured
// in Unicode characters, not bytes: "é" is 1 rune but 2 bytes in UTF-8, so
// a title of exactly maxTitleLen "é" characters is 2*maxTitleLen bytes —
// len() on the raw string would wrongly reject it as over the limit. This
// guards a real bug where a byte-length check rejected valid non-ASCII
// input well under the intended character limit (e.g. Cyrillic, CJK,
// emoji, accented Latin).
func TestCreateTask_TitleUnicode_AtMaxLength(t *testing.T) {
	svc := NewService(&fakeRepository{})

	title := strings.Repeat("é", maxTitleLen)
	got, err := svc.CreateTask(context.Background(), testUserID, title, "description", "")
	if err != nil {
		t.Fatalf("CreateTask() title at max rune length unexpected error: %v", err)
	}
	if got.Title != title {
		t.Errorf("CreateTask() Title = %q, want %q", got.Title, title)
	}
}

// TestCreateTask_TitleUnicode_OverMaxLength verifies that a title one rune
// over the limit is still rejected, even though — as bytes — it would be
// far more than one unit over.
func TestCreateTask_TitleUnicode_OverMaxLength(t *testing.T) {
	svc := NewService(&fakeRepository{})

	_, err := svc.CreateTask(context.Background(), testUserID, strings.Repeat("é", maxTitleLen+1), "description", "")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("CreateTask() overlong unicode title error = %v, want ErrInvalidInput", err)
	}
}

// TestCreateTask_DescriptionUnicode_AtMaxLength mirrors
// TestCreateTask_TitleUnicode_AtMaxLength for description.
func TestCreateTask_DescriptionUnicode_AtMaxLength(t *testing.T) {
	svc := NewService(&fakeRepository{})

	description := strings.Repeat("é", maxDescriptionLen)
	got, err := svc.CreateTask(context.Background(), testUserID, "Valid title", description, "")
	if err != nil {
		t.Fatalf("CreateTask() description at max rune length unexpected error: %v", err)
	}
	if got.Description != description {
		t.Errorf("CreateTask() Description = %q, want %q", got.Description, description)
	}
}

func TestCreateTask_TrimsTitleAndDescription(t *testing.T) {
	svc := NewService(&fakeRepository{})

	got, err := svc.CreateTask(context.Background(), testUserID, "  Padded title  ", "  Padded description  ", "")
	if err != nil {
		t.Fatalf("CreateTask() unexpected error: %v", err)
	}
	if got.Title != "Padded title" {
		t.Errorf("CreateTask() Title = %q, want %q", got.Title, "Padded title")
	}
	if got.Description != "Padded description" {
		t.Errorf("CreateTask() Description = %q, want %q", got.Description, "Padded description")
	}
}

func TestCreateTask_RepositoryError(t *testing.T) {
	repoErr := errors.New("storage failure")
	svc := NewService(&fakeRepository{createErr: repoErr})

	_, err := svc.CreateTask(context.Background(), testUserID, "Valid title", "", "")
	if !errors.Is(err, repoErr) {
		t.Errorf("CreateTask() repository error = %v, want %v", err, repoErr)
	}
}

// --- GetTask ---

func TestGetTask_Delegates(t *testing.T) {
	task := newFakeTask(StatusPending)
	svc := NewService(&fakeRepository{findByIDTask: task})

	got, err := svc.GetTask(context.Background(), testUserID, validTaskID)
	if err != nil {
		t.Fatalf("GetTask() unexpected error: %v", err)
	}
	if got.ID != task.ID {
		t.Errorf("GetTask() ID = %q, want %q", got.ID, task.ID)
	}
}

func TestGetTask_NotFound(t *testing.T) {
	svc := NewService(&fakeRepository{findByIDErr: ErrNotFound})

	_, err := svc.GetTask(context.Background(), testUserID, validTaskID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetTask() error = %v, want ErrNotFound", err)
	}
}

// --- ListTasks ---

func TestListTasks_Delegates(t *testing.T) {
	tasks := []Task{newFakeTask(StatusPending), newFakeTask(StatusDone)}
	svc := NewService(&fakeRepository{findAllTasks: tasks})

	got, err := svc.ListTasks(context.Background(), testUserID, -1, 0, "", "")
	if err != nil {
		t.Fatalf("ListTasks() unexpected error: %v", err)
	}
	if len(got) != len(tasks) {
		t.Errorf("ListTasks() returned %d tasks, want %d", len(got), len(tasks))
	}
}

// TestListTasks_PassesUserIDLimitOffsetFiltersToRepository verifies that
// Service passes userID, the limit/offset it received, and the parsed
// status/priority filters straight through to Repository.FindAll,
// unmodified — Service no longer applies its own ordering or windowing
// (that moved to Repository; see memory_repository_test.go's and
// postgres_repository_test.go's FindAll ordering/pagination/filter
// tests).
func TestListTasks_PassesUserIDLimitOffsetFiltersToRepository(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo)

	if _, err := svc.ListTasks(context.Background(), testUserID, 10, 5, "pending", "high"); err != nil {
		t.Fatalf("ListTasks() unexpected error: %v", err)
	}
	if want := [5]any{testUserID, 10, 5, StatusPending, PriorityHigh}; repo.findAllCalledWith != want {
		t.Errorf("ListTasks() called Repository.FindAll with (userID, limit, offset, status, priority) = %v, want %v", repo.findAllCalledWith, want)
	}
}

// TestListTasks_EmptyFiltersReachRepositoryAsEmpty verifies that omitted
// status/priority reach Repository.FindAll as the empty-string "no
// filter" sentinel, not some other zero value.
func TestListTasks_EmptyFiltersReachRepositoryAsEmpty(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo)

	if _, err := svc.ListTasks(context.Background(), testUserID, -1, 0, "", ""); err != nil {
		t.Fatalf("ListTasks() unexpected error: %v", err)
	}
	if want := [5]any{testUserID, -1, 0, Status(""), Priority("")}; repo.findAllCalledWith != want {
		t.Errorf("ListTasks() called Repository.FindAll with (userID, limit, offset, status, priority) = %v, want %v", repo.findAllCalledWith, want)
	}
}

func TestListTasks_RepositoryError(t *testing.T) {
	repoErr := errors.New("storage failure")
	svc := NewService(&fakeRepository{findAllErr: repoErr})

	_, err := svc.ListTasks(context.Background(), testUserID, -1, 0, "", "")
	if !errors.Is(err, repoErr) {
		t.Errorf("ListTasks() repository error = %v, want %v", err, repoErr)
	}
}

// TestListTasks_UnknownStatusFilterIsInvalidInput verifies that an
// unrecognized status filter is rejected before Repository is ever
// reached — Repository.FindAll must never see a value it doesn't know
// how to interpret.
func TestListTasks_UnknownStatusFilterIsInvalidInput(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo)

	_, err := svc.ListTasks(context.Background(), testUserID, -1, 0, "archived", "")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("ListTasks() with unknown status filter error = %v, want ErrInvalidInput", err)
	}
	if repo.findAllCalledWith != ([5]any{}) {
		t.Errorf("ListTasks() must not call Repository.FindAll for an invalid filter, called with %v", repo.findAllCalledWith)
	}
}

// TestListTasks_UnknownPriorityFilterIsInvalidInput mirrors
// TestListTasks_UnknownStatusFilterIsInvalidInput for priority.
func TestListTasks_UnknownPriorityFilterIsInvalidInput(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo)

	_, err := svc.ListTasks(context.Background(), testUserID, -1, 0, "", "urgent")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("ListTasks() with unknown priority filter error = %v, want ErrInvalidInput", err)
	}
	if repo.findAllCalledWith != ([5]any{}) {
		t.Errorf("ListTasks() must not call Repository.FindAll for an invalid filter, called with %v", repo.findAllCalledWith)
	}
}

// --- UpdateTask ---

func TestUpdateTask_ValidTitle(t *testing.T) {
	original := newFakeTask(StatusPending)
	repo := &fakeRepository{findByIDTask: original}
	svc := NewService(repo)

	got, err := svc.UpdateTask(context.Background(), testUserID, validTaskID, "New title", "New description", "")
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

func TestUpdateTask_EmptyPriority_KeepsCurrent(t *testing.T) {
	original := newFakeTask(StatusPending)
	original.Priority = PriorityHigh
	repo := &fakeRepository{findByIDTask: original}
	svc := NewService(repo)

	got, err := svc.UpdateTask(context.Background(), testUserID, validTaskID, "New title", "", "")
	if err != nil {
		t.Fatalf("UpdateTask() unexpected error: %v", err)
	}
	if got.Priority != PriorityHigh {
		t.Errorf("UpdateTask() with empty priority Priority = %q, want unchanged %q", got.Priority, PriorityHigh)
	}
}

func TestUpdateTask_ExplicitPriority_Changes(t *testing.T) {
	original := newFakeTask(StatusPending)
	original.Priority = PriorityLow
	repo := &fakeRepository{findByIDTask: original}
	svc := NewService(repo)

	got, err := svc.UpdateTask(context.Background(), testUserID, validTaskID, "New title", "", "high")
	if err != nil {
		t.Fatalf("UpdateTask() unexpected error: %v", err)
	}
	if got.Priority != PriorityHigh {
		t.Errorf("UpdateTask() Priority = %q, want %q", got.Priority, PriorityHigh)
	}
}

func TestUpdateTask_InvalidPriority(t *testing.T) {
	repo := &fakeRepository{findByIDTask: newFakeTask(StatusPending)}
	svc := NewService(repo)

	_, err := svc.UpdateTask(context.Background(), testUserID, validTaskID, "Title", "", "urgent")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("UpdateTask() invalid priority error = %v, want ErrInvalidInput", err)
	}
}

func TestUpdateTask_EmptyTitle(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo)

	_, err := svc.UpdateTask(context.Background(), testUserID, validTaskID, "", "desc", "")
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

	_, err := svc.UpdateTask(context.Background(), testUserID, validTaskID, "   ", "desc", "")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("UpdateTask() whitespace title error = %v, want ErrInvalidInput", err)
	}
	if repo.updateCalled {
		t.Error("UpdateTask() must not call Repository.Update on invalid input")
	}
}

func TestUpdateTask_NotFound(t *testing.T) {
	svc := NewService(&fakeRepository{findByIDErr: ErrNotFound})

	_, err := svc.UpdateTask(context.Background(), testUserID, validTaskID, "Title", "", "")
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

	_, err := svc.UpdateTask(context.Background(), testUserID, validTaskID, "Title", "", "")
	if !errors.Is(err, repoErr) {
		t.Errorf("UpdateTask() repository error = %v, want %v", err, repoErr)
	}
}

func TestUpdateTask_RepositoryConflictError(t *testing.T) {
	repo := &fakeRepository{
		findByIDTask: newFakeTask(StatusPending),
		updateErr:    ErrConflict,
	}
	svc := NewService(repo)

	_, err := svc.UpdateTask(context.Background(), testUserID, validTaskID, "Title", "", "")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("UpdateTask() error = %v, want ErrConflict", err)
	}
}

func TestUpdateTask_TitleTooLong(t *testing.T) {
	svc := NewService(&fakeRepository{})

	_, err := svc.UpdateTask(context.Background(), testUserID, validTaskID, strings.Repeat("a", maxTitleLen+1), "desc", "")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("UpdateTask() overlong title error = %v, want ErrInvalidInput", err)
	}
}

func TestUpdateTask_DescriptionTooLong(t *testing.T) {
	svc := NewService(&fakeRepository{})

	_, err := svc.UpdateTask(context.Background(), testUserID, validTaskID, "Title", strings.Repeat("a", maxDescriptionLen+1), "")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("UpdateTask() overlong description error = %v, want ErrInvalidInput", err)
	}
}

func TestUpdateTask_TrimsTitleAndDescription(t *testing.T) {
	repo := &fakeRepository{findByIDTask: newFakeTask(StatusPending)}
	svc := NewService(repo)

	got, err := svc.UpdateTask(context.Background(), testUserID, validTaskID, "  Padded title  ", "  Padded description  ", "")
	if err != nil {
		t.Fatalf("UpdateTask() unexpected error: %v", err)
	}
	if got.Title != "Padded title" {
		t.Errorf("UpdateTask() Title = %q, want %q", got.Title, "Padded title")
	}
	if got.Description != "Padded description" {
		t.Errorf("UpdateTask() Description = %q, want %q", got.Description, "Padded description")
	}
}

// --- DeleteTask ---

func TestDeleteTask_Delegates(t *testing.T) {
	svc := NewService(&fakeRepository{})

	if err := svc.DeleteTask(context.Background(), testUserID, validTaskID); err != nil {
		t.Errorf("DeleteTask() unexpected error: %v", err)
	}
}

func TestDeleteTask_NotFound(t *testing.T) {
	svc := NewService(&fakeRepository{deleteErr: ErrNotFound})

	err := svc.DeleteTask(context.Background(), testUserID, validTaskID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteTask() error = %v, want ErrNotFound", err)
	}
}

func TestDeleteTask_RepositoryError(t *testing.T) {
	repoErr := errors.New("delete failure")
	svc := NewService(&fakeRepository{deleteErr: repoErr})

	err := svc.DeleteTask(context.Background(), testUserID, validTaskID)
	if !errors.Is(err, repoErr) {
		t.Errorf("DeleteTask() repository error = %v, want %v", err, repoErr)
	}
}

// --- CompleteTask ---

func TestCompleteTask_PendingToDone(t *testing.T) {
	original := newFakeTask(StatusPending)
	repo := &fakeRepository{findByIDTask: original}
	svc := NewService(repo)

	got, err := svc.CompleteTask(context.Background(), testUserID, validTaskID)
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

	got, err := svc.CompleteTask(context.Background(), testUserID, validTaskID)
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

	_, err := svc.CompleteTask(context.Background(), testUserID, validTaskID)
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

	_, err := svc.CompleteTask(context.Background(), testUserID, validTaskID)
	if !errors.Is(err, repoErr) {
		t.Errorf("CompleteTask() repository error = %v, want %v", err, repoErr)
	}
}

func TestCompleteTask_RepositoryConflictError(t *testing.T) {
	repo := &fakeRepository{
		findByIDTask: newFakeTask(StatusPending),
		updateErr:    ErrConflict,
	}
	svc := NewService(repo)

	_, err := svc.CompleteTask(context.Background(), testUserID, validTaskID)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("CompleteTask() error = %v, want ErrConflict", err)
	}
}

// --- TransitionStatus ---

// TestTransitionStatus_LegalTransitions exercises every transition
// legalTransitions marks as allowed (see service.go's table).
func TestTransitionStatus_LegalTransitions(t *testing.T) {
	cases := []struct {
		from, to Status
	}{
		{StatusPending, StatusInProgress},
		{StatusPending, StatusDone},
		{StatusPending, StatusCancelled},
		{StatusInProgress, StatusPending},
		{StatusInProgress, StatusDone},
		{StatusInProgress, StatusCancelled},
		{StatusDone, StatusPending},
		{StatusDone, StatusInProgress},
		{StatusCancelled, StatusPending},
	}

	for _, tc := range cases {
		t.Run(string(tc.from)+"_to_"+string(tc.to), func(t *testing.T) {
			repo := &fakeRepository{findByIDTask: newFakeTask(tc.from)}
			svc := NewService(repo)

			got, err := svc.TransitionStatus(context.Background(), testUserID, validTaskID, tc.to)
			if err != nil {
				t.Fatalf("TransitionStatus(%s -> %s) unexpected error: %v", tc.from, tc.to, err)
			}
			if got.Status != tc.to {
				t.Errorf("TransitionStatus(%s -> %s) Status = %q, want %q", tc.from, tc.to, got.Status, tc.to)
			}
			if !repo.updateCalled {
				t.Errorf("TransitionStatus(%s -> %s) must call Repository.Update", tc.from, tc.to)
			}
		})
	}
}

// TestTransitionStatus_IllegalTransitions exercises every transition
// legalTransitions marks as disallowed.
func TestTransitionStatus_IllegalTransitions(t *testing.T) {
	cases := []struct {
		from, to Status
	}{
		{StatusDone, StatusCancelled},
		{StatusCancelled, StatusInProgress},
		{StatusCancelled, StatusDone},
	}

	for _, tc := range cases {
		t.Run(string(tc.from)+"_to_"+string(tc.to), func(t *testing.T) {
			repo := &fakeRepository{findByIDTask: newFakeTask(tc.from)}
			svc := NewService(repo)

			_, err := svc.TransitionStatus(context.Background(), testUserID, validTaskID, tc.to)
			if !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("TransitionStatus(%s -> %s) error = %v, want ErrInvalidTransition", tc.from, tc.to, err)
			}
			if repo.updateCalled {
				t.Errorf("TransitionStatus(%s -> %s) must not call Repository.Update on an illegal transition", tc.from, tc.to)
			}
		})
	}
}

// TestTransitionStatus_SameStatus_Idempotent verifies that requesting a
// task's current status again is always a no-op success, independent of
// legalTransitions — mirroring CompleteTask's existing idempotency for
// "already done".
func TestTransitionStatus_SameStatus_Idempotent(t *testing.T) {
	original := newFakeTask(StatusInProgress)
	repo := &fakeRepository{findByIDTask: original}
	svc := NewService(repo)

	got, err := svc.TransitionStatus(context.Background(), testUserID, validTaskID, StatusInProgress)
	if err != nil {
		t.Fatalf("TransitionStatus() same-status unexpected error: %v", err)
	}
	if !got.UpdatedAt.Equal(original.UpdatedAt) {
		t.Errorf("TransitionStatus() idempotent: UpdatedAt changed from %v to %v", original.UpdatedAt, got.UpdatedAt)
	}
	if repo.updateCalled {
		t.Error("TransitionStatus() must NOT call Repository.Update for a same-status request")
	}
}

func TestTransitionStatus_UnknownStatus(t *testing.T) {
	repo := &fakeRepository{findByIDTask: newFakeTask(StatusPending)}
	svc := NewService(repo)

	_, err := svc.TransitionStatus(context.Background(), testUserID, validTaskID, Status("archived"))
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("TransitionStatus() unknown status error = %v, want ErrInvalidInput", err)
	}
	if repo.updateCalled {
		t.Error("TransitionStatus() must not call Repository.Update for an unknown status")
	}
}

func TestTransitionStatus_NotFound(t *testing.T) {
	svc := NewService(&fakeRepository{findByIDErr: ErrNotFound})

	_, err := svc.TransitionStatus(context.Background(), testUserID, validTaskID, StatusDone)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("TransitionStatus() error = %v, want ErrNotFound", err)
	}
}

// Note: GET /tasks' ordering guarantee (CreatedAt ascending, ties broken by
// ID) is no longer applied by Service — it's Repository.FindAll's own
// contract now, enforced and tested at that layer directly: see
// TestFindAll_OrdersByCreatedAtThenID in memory_repository_test.go and
// TestPostgres_FindAll_OrderedByCreatedAtThenID in postgres_repository_test.go.

// --- input normalization ---

// TestCreateTask_StripsControlCharactersFromTitle covers the characters
// that are invisible in the request body and consequential once stored: a
// NUL truncates the value for anything that later treats it as a C
// string, and a raw escape sequence rewrites a terminal that prints a log
// line carrying it.
func TestCreateTask_StripsControlCharactersFromTitle(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"NUL", "Buy\x00 milk", "Buy milk"},
		{"escape sequence", "Buy \x1b[31mmilk", "Buy [31mmilk"},
		{"C1 control", "Buy \u0085milk", "Buy milk"},
		{"zero-width joiner is left alone", "Buy \u200dmilk", "Buy \u200dmilk"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeRepository{})

			task, err := svc.CreateTask(context.Background(), "u1", tt.input, "", "")
			if err != nil {
				t.Fatalf("CreateTask() unexpected error: %v", err)
			}
			if task.Title != tt.want {
				t.Errorf("Title = %q, want %q", task.Title, tt.want)
			}
		})
	}
}

// TestCreateTask_CollapsesWhitespaceInTitleOnly pins the asymmetry
// between the two fields: a title is one line of display text, a
// description is prose whose line breaks are content the user typed.
func TestCreateTask_CollapsesWhitespaceInTitleOnly(t *testing.T) {
	svc := NewService(&fakeRepository{})

	task, err := svc.CreateTask(context.Background(), "u1",
		"Buy    milk\tand\neggs",
		"First paragraph.\n\nSecond paragraph.\n\t- indented item",
		"")
	if err != nil {
		t.Fatalf("CreateTask() unexpected error: %v", err)
	}

	if want := "Buy milk and eggs"; task.Title != want {
		t.Errorf("Title = %q, want %q", task.Title, want)
	}
	if want := "First paragraph.\n\nSecond paragraph.\n\t- indented item"; task.Description != want {
		t.Errorf("Description = %q, want it preserved verbatim (%q)", task.Description, want)
	}
}

// TestCreateTask_DescriptionKeepsLineBreaksButLosesControlChars is the
// other half of the asymmetry: stripping still happens in a description,
// it just spares the three whitespace characters that carry meaning.
func TestCreateTask_DescriptionKeepsLineBreaksButLosesControlChars(t *testing.T) {
	svc := NewService(&fakeRepository{})

	task, err := svc.CreateTask(context.Background(), "u1", "Title",
		"line one\n\x00line two\x1b", "")
	if err != nil {
		t.Fatalf("CreateTask() unexpected error: %v", err)
	}

	if want := "line one\nline two"; task.Description != want {
		t.Errorf("Description = %q, want %q", task.Description, want)
	}
}

// TestCreateTask_TitleOfOnlyControlCharactersIsRejected closes the gap
// between stripping and validating: a title that is non-empty in the
// request but empty once normalized must fail the same way an empty one
// does, rather than being stored blank.
func TestCreateTask_TitleOfOnlyControlCharactersIsRejected(t *testing.T) {
	svc := NewService(&fakeRepository{})

	_, err := svc.CreateTask(context.Background(), "u1", "\x00\x1b \t", "", "")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("CreateTask() error = %v, want ErrInvalidInput", err)
	}
}

// TestCreateTask_LengthIsMeasuredAfterNormalization pins the ordering:
// a title that only exceeds the limit because of characters that get
// stripped must be accepted, since the limit applies to what is stored.
func TestCreateTask_LengthIsMeasuredAfterNormalization(t *testing.T) {
	svc := NewService(&fakeRepository{})

	title := strings.Repeat("a", maxTitleLen) + strings.Repeat("\x00", 50)

	task, err := svc.CreateTask(context.Background(), "u1", title, "", "")
	if err != nil {
		t.Fatalf("CreateTask() unexpected error: %v", err)
	}
	if got := utf8.RuneCountInString(task.Title); got != maxTitleLen {
		t.Errorf("stored title length = %d, want %d", got, maxTitleLen)
	}
}

// TestCreateTask_DoesNotStripMarkup guards the boundary the sanitize
// package is explicit about: this is normalization, not an XSS defense.
// Mangling markup here would corrupt legitimate input while providing no
// real protection — encoding at the point of rendering is what does that.
func TestCreateTask_DoesNotStripMarkup(t *testing.T) {
	svc := NewService(&fakeRepository{})

	const title = `Ben <the> Third & "co"`

	task, err := svc.CreateTask(context.Background(), "u1", title, "", "")
	if err != nil {
		t.Fatalf("CreateTask() unexpected error: %v", err)
	}
	if task.Title != title {
		t.Errorf("Title = %q, want it stored verbatim (%q)", task.Title, title)
	}
}

// TestUpdateTask_NormalizesToo covers the other write path — the two
// share validateTitleAndDescription, and a future refactor that gave
// UpdateTask its own validation would silently skip normalization.
func TestUpdateTask_NormalizesToo(t *testing.T) {
	repo := &fakeRepository{
		findByIDTask: Task{ID: "t1", UserID: "u1", Title: "Original", Status: StatusPending},
	}
	svc := NewService(repo)

	updated, err := svc.UpdateTask(context.Background(), "u1", validTaskID, "Buy\x00 milk", "desc", "")
	if err != nil {
		t.Fatalf("UpdateTask() unexpected error: %v", err)
	}
	if want := "Buy milk"; updated.Title != want {
		t.Errorf("Title = %q, want %q", updated.Title, want)
	}
}

// TestCreateTask_LeavesBidirectionalFormatCharacters records a limit of
// the current normalization rather than endorsing it: category Cf
// characters — including the bidirectional overrides behind Trojan
// Source-style spoofing — are not stripped, because the same block
// contains the isolates that are the correct way to embed Hebrew or
// Arabic in Latin text. Removing them wholesale would corrupt legitimate
// input from the users who most need it.
//
// If a rule for these is ever added, this test is the one that should
// fail and be rewritten, rather than the behavior changing unnoticed.
func TestCreateTask_LeavesBidirectionalFormatCharacters(t *testing.T) {
	svc := NewService(&fakeRepository{})

	const title = "Buy \u202emilk"

	task, err := svc.CreateTask(context.Background(), "u1", title, "", "")
	if err != nil {
		t.Fatalf("CreateTask() unexpected error: %v", err)
	}
	if task.Title != title {
		t.Errorf("Title = %q, want %q — Cf characters are deliberately out of scope", task.Title, title)
	}
}

// --- Malformed task IDs ---
//
// These pin the fix for a real, previously-shipped inconsistency: an ID
// that is not a syntactically valid UUID reached postgresRepository's
// ::uuid cast, which rejected it as a query error surfacing as 500 —
// while memoryRepository answered the identical input with ErrNotFound
// (404). isValidID closes that gap by rejecting the shape before either
// Repository is ever consulted, so both agree by construction rather
// than by coincidence. See TestPostgres_FindByID_MalformedID for what
// Repository still does when called directly, bypassing Service.

// malformedTaskIDs is the shared table every test below runs against.
// Each case is a different way of not being a UUID: empty, wrong
// length, wrong grouping, and a well-formed length with an
// out-of-alphabet character — the kind of thing a client-side typo
// actually produces, not an adversarial input.
var malformedTaskIDs = []struct {
	name string
	id   string
}{
	{"empty", ""},
	{"not_uuid_shaped_at_all", "not-a-uuid"},
	{"too_short", "11111111-1111-1111-1111-11111111"},
	{"too_long", "11111111-1111-1111-1111-1111111111111"},
	{"missing_hyphens", "11111111111111111111111111111111"},
	{"wrong_grouping", "111111111-111-1111-1111-111111111111"},
	{"non_hex_character", "1111111g-1111-1111-1111-111111111111"},
	{"sql_injection_attempt", "'; DROP TABLE tasks; --"},
}

func TestGetTask_MalformedID_IsNotFound(t *testing.T) {
	for _, tc := range malformedTaskIDs {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepository{}
			svc := NewService(repo)

			_, err := svc.GetTask(context.Background(), testUserID, tc.id)
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("GetTask(%q) error = %v, want ErrNotFound", tc.id, err)
			}
			if repo.findByIDCalled {
				t.Errorf("GetTask(%q) reached Repository.FindByID; isValidID should have rejected it first", tc.id)
			}
		})
	}
}

func TestUpdateTask_MalformedID_IsNotFound(t *testing.T) {
	for _, tc := range malformedTaskIDs {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepository{}
			svc := NewService(repo)

			_, err := svc.UpdateTask(context.Background(), testUserID, tc.id, "Title", "", "")
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("UpdateTask(%q) error = %v, want ErrNotFound", tc.id, err)
			}
			if repo.findByIDCalled || repo.updateCalled {
				t.Errorf("UpdateTask(%q) reached Repository; isValidID should have rejected it first", tc.id)
			}
		})
	}
}

func TestDeleteTask_MalformedID_IsNotFound(t *testing.T) {
	for _, tc := range malformedTaskIDs {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepository{}
			svc := NewService(repo)

			err := svc.DeleteTask(context.Background(), testUserID, tc.id)
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("DeleteTask(%q) error = %v, want ErrNotFound", tc.id, err)
			}
			if repo.deleteCalled {
				t.Errorf("DeleteTask(%q) reached Repository.Delete; isValidID should have rejected it first", tc.id)
			}
		})
	}
}

func TestTransitionStatus_MalformedID_IsNotFound(t *testing.T) {
	for _, tc := range malformedTaskIDs {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepository{}
			svc := NewService(repo)

			_, err := svc.TransitionStatus(context.Background(), testUserID, tc.id, StatusDone)
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("TransitionStatus(%q) error = %v, want ErrNotFound", tc.id, err)
			}
			if repo.findByIDCalled {
				t.Errorf("TransitionStatus(%q) reached Repository.FindByID; isValidID should have rejected it first", tc.id)
			}
		})
	}
}

// TestCompleteTask_MalformedID_IsNotFound covers CompleteTask
// specifically, even though it is a thin wrapper around
// TransitionStatus: PATCH /tasks/{id}/done is the older, still-documented
// route, and a wrapper that stopped delegating correctly would otherwise
// go unnoticed here.
func TestCompleteTask_MalformedID_IsNotFound(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo)

	_, err := svc.CompleteTask(context.Background(), testUserID, "not-a-uuid")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("CompleteTask() error = %v, want ErrNotFound", err)
	}
	if repo.findByIDCalled {
		t.Error("CompleteTask() reached Repository.FindByID; isValidID should have rejected it first")
	}
}

// TestGetTask_WellFormedID_StillReachesRepository is the control case
// for every test above: a syntactically valid UUID that simply isn't
// stored anywhere must still reach Repository and get ErrNotFound from
// it, not from isValidID. Without this, the tests above could pass for
// the wrong reason — isValidID rejecting everything, valid IDs included.
func TestGetTask_WellFormedID_StillReachesRepository(t *testing.T) {
	repo := &fakeRepository{findByIDErr: ErrNotFound}
	svc := NewService(repo)

	_, err := svc.GetTask(context.Background(), testUserID, validTaskID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetTask() error = %v, want ErrNotFound", err)
	}
	if !repo.findByIDCalled {
		t.Error("GetTask() with a well-formed ID did not reach Repository.FindByID")
	}
}
