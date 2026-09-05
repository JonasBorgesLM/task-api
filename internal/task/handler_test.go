package task

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JonasBorgesLM/task-api/internal/middleware"
)

// fakeService is a test double for taskService.
type fakeService struct {
	createTaskFn       func(userID, title, description, priority string) (Task, error)
	getTaskFn          func(userID, id string) (Task, error)
	listTasksFn        func(userID string, limit, offset int, statuses, priorities []string) ([]Task, error)
	updateTaskFn       func(userID, id, title, description, priority string) (Task, error)
	deleteTaskFn       func(userID, id string) error
	completeTaskFn     func(userID, id string) (Task, error)
	transitionStatusFn func(userID, id string, target Status) (Task, error)

	// Call recording.
	createCalledWith     [4]string // [userID, title, description, priority]
	listCalledWith       [5]any    // [userID, limit, offset, statuses, priorities]
	updateCalledWith     [5]string // [userID, id, title, description, priority]
	completeCalledWith   [2]string // [userID, id]
	deleteCalledWith     [2]string // [userID, id]
	transitionCalledWith [3]any    // [userID, id, target]
}

func (f *fakeService) CreateTask(_ context.Context, userID, title, description, priority string) (Task, error) {
	f.createCalledWith = [4]string{userID, title, description, priority}
	if f.createTaskFn != nil {
		return f.createTaskFn(userID, title, description, priority)
	}
	return Task{}, nil
}

func (f *fakeService) GetTask(_ context.Context, userID, id string) (Task, error) {
	if f.getTaskFn != nil {
		return f.getTaskFn(userID, id)
	}
	return Task{}, nil
}

func (f *fakeService) ListTasks(_ context.Context, userID string, limit, offset int, statuses, priorities []string) ([]Task, error) {
	// Joined rather than stored as slices: [5]any is compared with ==,
	// which panics on a slice. The separator is a character no status or
	// priority contains, so "pending,done" is unambiguous.
	f.listCalledWith = [5]any{userID, limit, offset, strings.Join(statuses, ","), strings.Join(priorities, ",")}
	if f.listTasksFn != nil {
		return f.listTasksFn(userID, limit, offset, statuses, priorities)
	}
	return []Task{}, nil
}

func (f *fakeService) UpdateTask(_ context.Context, userID, id, title, description, priority string) (Task, error) {
	f.updateCalledWith = [5]string{userID, id, title, description, priority}
	if f.updateTaskFn != nil {
		return f.updateTaskFn(userID, id, title, description, priority)
	}
	return Task{}, nil
}

func (f *fakeService) DeleteTask(_ context.Context, userID, id string) error {
	f.deleteCalledWith = [2]string{userID, id}
	if f.deleteTaskFn != nil {
		return f.deleteTaskFn(userID, id)
	}
	return nil
}

func (f *fakeService) CompleteTask(_ context.Context, userID, id string) (Task, error) {
	f.completeCalledWith = [2]string{userID, id}
	if f.completeTaskFn != nil {
		return f.completeTaskFn(userID, id)
	}
	return Task{}, nil
}

func (f *fakeService) TransitionStatus(_ context.Context, userID, id string, target Status) (Task, error) {
	f.transitionCalledWith = [3]any{userID, id, target}
	if f.transitionStatusFn != nil {
		return f.transitionStatusFn(userID, id, target)
	}
	return Task{}, nil
}

// sampleTask returns a Task with predictable values for use in handler tests.
func sampleTask() Task {
	now := time.Now()
	return Task{
		ID:          "abc-123",
		UserID:      testUserID,
		Title:       "Sample task",
		Description: "Sample description",
		Status:      StatusPending,
		Priority:    PriorityMedium,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// newHandlerWithFake wires a fakeService into a Handler using NewHandler.
// A discard logger is used to suppress log output during tests.
func newHandlerWithFake(svc *fakeService) *Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHandler(svc, logger)
}

// newHandlerWithFakeLogged is like newHandlerWithFake, but wires a JSON
// logger that writes to the returned buffer instead of discarding output,
// for tests that need to inspect what Handler itself logged.
func newHandlerWithFakeLogged(svc *fakeService) (*Handler, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	return NewHandler(svc, logger), &buf
}

// passthroughAuth is a stand-in for user.RequireAuth in tests that only
// need RegisterRoutes wired up (e.g. TestRegisterRoutes): it never
// rejects a request, and always injects testUserID into the request
// context, the same context key real auth middleware sets on success.
func passthroughAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := middleware.ContextWithUserID(r.Context(), testUserID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// do executes an HTTP request against the given handler function and
// returns the recorded response. The request context always carries
// testUserID, standing in for what a real requireAuth middleware would
// have set — every handler method under test reads it via
// middleware.UserIDFromContext, exactly as it would in production once
// RegisterRoutes' requireAuth wrapper has run.
func do(t *testing.T, handler http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), testUserID))
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

// decodeBody is a helper that decodes the response body into dst.
func decodeBody(t *testing.T, w *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.NewDecoder(w.Body).Decode(dst); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
}

// --- POST /tasks ---

func TestCreateTask_Handler_ValidJSON(t *testing.T) {
	task := sampleTask()
	svc := &fakeService{
		createTaskFn: func(userID, title, description, priority string) (Task, error) {
			return task, nil
		},
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.createTask, http.MethodPost, "/tasks",
		`{"title":"Sample task","description":"Sample description"}`)

	if w.Code != http.StatusCreated {
		t.Errorf("createTask status = %d, want %d", w.Code, http.StatusCreated)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("createTask Content-Type = %q, want application/json", ct)
	}
	if svc.createCalledWith[0] != testUserID {
		t.Errorf("createTask userID sent to service = %q, want %q", svc.createCalledWith[0], testUserID)
	}

	var got Task
	decodeBody(t, w, &got)
	if got.ID != task.ID {
		t.Errorf("createTask body ID = %q, want %q", got.ID, task.ID)
	}
}

func TestCreateTask_Handler_InvalidJSON(t *testing.T) {
	h := newHandlerWithFake(&fakeService{})

	w := do(t, h.createTask, http.MethodPost, "/tasks", `{invalid}`)

	if w.Code != http.StatusBadRequest {
		t.Errorf("createTask invalid JSON status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var body map[string]string
	decodeBody(t, w, &body)
	if _, ok := body["error"]; !ok {
		t.Error("createTask invalid JSON response must contain 'error' key")
	}
}

func TestCreateTask_Handler_ErrInvalidInput(t *testing.T) {
	svc := &fakeService{
		createTaskFn: func(_, _, _, _ string) (Task, error) {
			return Task{}, fmt.Errorf("%w: title must not be empty", ErrInvalidInput)
		},
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.createTask, http.MethodPost, "/tasks", `{"title":""}`)

	if w.Code != http.StatusBadRequest {
		t.Errorf("createTask ErrInvalidInput status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateTask_Handler_IgnoresProtectedFields(t *testing.T) {
	svc := &fakeService{
		createTaskFn: func(userID, title, description, priority string) (Task, error) {
			return Task{UserID: userID, Title: title, Description: description, Status: StatusPending}, nil
		},
	}
	h := newHandlerWithFake(svc)

	// Client attempts to inject protected fields.
	w := do(t, h.createTask, http.MethodPost, "/tasks",
		`{"title":"T","description":"D","id":"injected","status":"done","user_id":"someone-else","created_at":"2000-01-01T00:00:00Z"}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("createTask status = %d, want %d", w.Code, http.StatusCreated)
	}

	// The service was called only with the allowed fields.
	if svc.createCalledWith[1] != "T" {
		t.Errorf("createTask title sent to service = %q, want %q", svc.createCalledWith[1], "T")
	}
	if svc.createCalledWith[0] != testUserID {
		t.Errorf("createTask userID sent to service = %q, want the authenticated caller %q, not a client-supplied value", svc.createCalledWith[0], testUserID)
	}

	var got Task
	decodeBody(t, w, &got)

	// Protected fields were not set by the client.
	if got.ID == "injected" {
		t.Error("createTask must not accept client-provided ID")
	}
	if got.Status == StatusDone {
		t.Error("createTask must not accept client-provided Status")
	}
}

// --- GET /tasks ---

func TestListTasks_Handler_ReturnsOK(t *testing.T) {
	tasks := []Task{sampleTask(), sampleTask()}
	svc := &fakeService{
		listTasksFn: func(userID string, limit, offset int, statuses, priorities []string) ([]Task, error) {
			return tasks, nil
		},
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.listTasks, http.MethodGet, "/tasks", "")

	if w.Code != http.StatusOK {
		t.Errorf("listTasks status = %d, want %d", w.Code, http.StatusOK)
	}

	var got []Task
	decodeBody(t, w, &got)
	if len(got) != len(tasks) {
		t.Errorf("listTasks returned %d tasks, want %d", len(got), len(tasks))
	}
}

func TestListTasks_Handler_EmptyIsArray(t *testing.T) {
	svc := &fakeService{
		listTasksFn: func(userID string, limit, offset int, statuses, priorities []string) ([]Task, error) { return nil, nil },
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.listTasks, http.MethodGet, "/tasks", "")

	if w.Code != http.StatusOK {
		t.Errorf("listTasks empty status = %d, want %d", w.Code, http.StatusOK)
	}

	// Body must be "[]" not "null".
	body := strings.TrimSpace(w.Body.String())
	if body == "null" {
		t.Error("listTasks empty result must be [] not null")
	}

	var got []Task
	decodeBody(t, w, &got)
	if got == nil {
		t.Error("listTasks empty result must decode as non-nil slice")
	}
}

// TestListTasks_Handler_PassesUserIDLimitOffsetFiltersToService verifies
// that Handler parses "limit"/"offset"/"status"/"priority" from the query
// string, reads the authenticated userID from context, and passes all
// five straight through to Service.ListTasks — the actual windowing/
// filtering (which IDs come back for a given limit/offset/status/priority)
// now happens inside Repository.FindAll, and is tested there directly:
// see TestFindAll_Pagination/TestFindAll_FiltersByStatusAndPriority in
// memory_repository_test.go and their PostgreSQL counterparts.
func TestListTasks_Handler_PassesUserIDLimitOffsetFiltersToService(t *testing.T) {
	svc := &fakeService{
		listTasksFn: func(userID string, limit, offset int, statuses, priorities []string) ([]Task, error) {
			return []Task{}, nil
		},
	}
	h := newHandlerWithFake(svc)

	cases := []struct {
		name         string
		query        string
		wantLimit    int
		wantOffset   int
		wantStatus   string
		wantPriority string
	}{
		{"no params means no limit, zero offset, no filter", "", -1, 0, "", ""},
		{"limit only", "?limit=2", 2, 0, "", ""},
		{"offset only", "?offset=3", -1, 3, "", ""},
		{"limit and offset", "?limit=2&offset=1", 2, 1, "", ""},
		{"limit zero", "?limit=0", 0, 0, "", ""},
		{"status only", "?status=pending", -1, 0, "pending", ""},
		{"priority only", "?priority=high", -1, 0, "", "high"},
		{"status and priority combined", "?status=done&priority=low", -1, 0, "done", "low"},
		{"status, priority, limit and offset combined", "?status=done&priority=low&limit=2&offset=1", 2, 1, "done", "low"},
		// Repeating a parameter asks for more than one at once; the
		// values within a field are an OR, the two fields an AND.
		{"status repeated", "?status=pending&status=done", -1, 0, "pending,done", ""},
		{"priority repeated", "?priority=high&priority=low", -1, 0, "", "high,low"},
		{
			"both repeated, with a window",
			"?status=pending&status=in_progress&priority=high&priority=medium&limit=2&offset=1",
			2, 1, "pending,in_progress", "high,medium",
		},
		// The handler hands the raw values over untouched, empties
		// included — dropping them is Service's job, asserted in
		// service_test.go's TestListTasks_EmptyFilterValuesAreDropped.
		// This case pins the pass-through, not the filtering.
		{"empty occurrence is passed through, not swallowed here", "?status=&status=done", -1, 0, ",done", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := do(t, h.listTasks, http.MethodGet, "/tasks"+tc.query, "")

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
			}

			want := [5]any{testUserID, tc.wantLimit, tc.wantOffset, tc.wantStatus, tc.wantPriority}
			if svc.listCalledWith != want {
				t.Errorf("ListTasks() called with (userID, limit, offset, status, priority) = %v, want %v", svc.listCalledWith, want)
			}
		})
	}
}

func TestListTasks_Handler_InvalidPaginationParams(t *testing.T) {
	svc := &fakeService{
		listTasksFn: func(userID string, limit, offset int, statuses, priorities []string) ([]Task, error) {
			return []Task{{ID: "1"}}, nil
		},
	}
	h := newHandlerWithFake(svc)

	cases := []string{
		"?limit=not-a-number",
		"?limit=-1",
		"?offset=not-a-number",
		"?offset=-1",
	}

	for _, query := range cases {
		t.Run(query, func(t *testing.T) {
			w := do(t, h.listTasks, http.MethodGet, "/tasks"+query, "")

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

// TestListTasks_Handler_InvalidFilterIs400 verifies that a Service error
// for an unrecognized status/priority filter value (ErrInvalidInput) maps
// to 400 through handleServiceError, the same as any other invalid-input
// rejection — Service.ListTasks itself validates the values (see
// TestListTasks_UnknownStatusFilterIsInvalidInput/
// TestListTasks_UnknownPriorityFilterIsInvalidInput in service_test.go);
// this only confirms the handler's wiring passes the error through.
func TestListTasks_Handler_InvalidFilterIs400(t *testing.T) {
	svc := &fakeService{
		listTasksFn: func(userID string, limit, offset int, statuses, priorities []string) ([]Task, error) {
			return nil, fmt.Errorf("%w: status must be one of pending, in_progress, done, cancelled", ErrInvalidInput)
		},
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.listTasks, http.MethodGet, "/tasks?status=archived", "")

	if w.Code != http.StatusBadRequest {
		t.Errorf("listTasks with invalid status filter = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// --- GET /tasks/{id} ---

func TestGetTask_Handler_Found(t *testing.T) {
	task := sampleTask()
	svc := &fakeService{
		getTaskFn: func(userID, id string) (Task, error) { return task, nil },
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.getTask, http.MethodGet, "/tasks/abc-123", "")

	if w.Code != http.StatusOK {
		t.Errorf("getTask status = %d, want %d", w.Code, http.StatusOK)
	}

	var got Task
	decodeBody(t, w, &got)
	if got.ID != task.ID {
		t.Errorf("getTask body ID = %q, want %q", got.ID, task.ID)
	}
}

func TestGetTask_Handler_NotFound(t *testing.T) {
	svc := &fakeService{
		getTaskFn: func(_, _ string) (Task, error) { return Task{}, ErrNotFound },
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.getTask, http.MethodGet, "/tasks/nonexistent", "")

	if w.Code != http.StatusNotFound {
		t.Errorf("getTask not found status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// --- PUT /tasks/{id} ---

func TestUpdateTask_Handler_ReturnsOK(t *testing.T) {
	task := sampleTask()
	task.Title = "Updated title"
	svc := &fakeService{
		updateTaskFn: func(userID, id, title, description, priority string) (Task, error) { return task, nil },
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.updateTask, http.MethodPut, "/tasks/abc-123",
		`{"title":"Updated title","description":"Updated desc"}`)

	if w.Code != http.StatusOK {
		t.Errorf("updateTask status = %d, want %d", w.Code, http.StatusOK)
	}

	var got Task
	decodeBody(t, w, &got)
	if got.Title != "Updated title" {
		t.Errorf("updateTask body Title = %q, want %q", got.Title, "Updated title")
	}
}

func TestUpdateTask_Handler_IgnoresProtectedFields(t *testing.T) {
	svc := &fakeService{
		updateTaskFn: func(userID, id, title, description, priority string) (Task, error) {
			return Task{ID: id, UserID: userID, Title: title, Description: description, Status: StatusPending}, nil
		},
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.updateTask, http.MethodPut, "/tasks/abc-123",
		`{"title":"T","description":"D","status":"done","id":"injected"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("updateTask status = %d, want %d", w.Code, http.StatusOK)
	}

	// Service was called with the correct fields only.
	if svc.updateCalledWith[2] != "T" {
		t.Errorf("updateTask title sent to service = %q, want %q", svc.updateCalledWith[2], "T")
	}

	var got Task
	decodeBody(t, w, &got)
	if got.Status == StatusDone {
		t.Error("updateTask must not accept client-provided Status")
	}
}

// --- PATCH /tasks/{id}/done ---

func TestCompleteTask_Handler_ReturnsOK(t *testing.T) {
	task := sampleTask()
	task.Status = StatusDone
	svc := &fakeService{
		completeTaskFn: func(userID, id string) (Task, error) { return task, nil },
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.completeTask, http.MethodPatch, "/tasks/abc-123/done", "")

	if w.Code != http.StatusOK {
		t.Errorf("completeTask status = %d, want %d", w.Code, http.StatusOK)
	}

	var got Task
	decodeBody(t, w, &got)
	if got.Status != StatusDone {
		t.Errorf("completeTask body Status = %q, want %q", got.Status, StatusDone)
	}
}

// --- PATCH /tasks/{id}/status ---

// TestTransitionStatus_Handler_ReturnsOK routes through a real ServeMux
// (unlike most handler tests, which call the method directly) because it
// asserts on the {id} path value RegisterRoutes' pattern extracts — that
// only gets populated by ServeMux's own routing, never by invoking a
// handler method directly with an httptest.Request.
func TestTransitionStatus_Handler_ReturnsOK(t *testing.T) {
	task := sampleTask()
	task.Status = StatusInProgress
	svc := &fakeService{
		transitionStatusFn: func(userID, id string, target Status) (Task, error) { return task, nil },
	}
	h := newHandlerWithFake(svc)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, passthroughAuth)

	req := httptest.NewRequest(http.MethodPatch, "/tasks/abc-123/status", strings.NewReader(`{"status":"in_progress"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("transitionStatus status = %d, want %d", w.Code, http.StatusOK)
	}
	want := [3]any{testUserID, "abc-123", StatusInProgress}
	if svc.transitionCalledWith != want {
		t.Errorf("TransitionStatus() called with %v, want %v", svc.transitionCalledWith, want)
	}

	var got Task
	decodeBody(t, w, &got)
	if got.Status != StatusInProgress {
		t.Errorf("transitionStatus body Status = %q, want %q", got.Status, StatusInProgress)
	}
}

func TestTransitionStatus_Handler_InvalidTransition_Returns409(t *testing.T) {
	svc := &fakeService{
		transitionStatusFn: func(_, _ string, _ Status) (Task, error) {
			return Task{}, fmt.Errorf("%w: cannot move from %q to %q", ErrInvalidTransition, StatusDone, StatusCancelled)
		},
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.transitionStatus, http.MethodPatch, "/tasks/abc-123/status", `{"status":"cancelled"}`)

	if w.Code != http.StatusConflict {
		t.Errorf("transitionStatus invalid transition status = %d, want %d", w.Code, http.StatusConflict)
	}

	var body errorResponse
	decodeBody(t, w, &body)
	if body.Reason != "invalid_transition" {
		t.Errorf(`transitionStatus invalid transition body reason = %q, want "invalid_transition" (issue #153)`, body.Reason)
	}
}

// TestTransitionStatus_Handler_Conflict_Returns409WithReason is
// InvalidTransition's counterpart: the *other* condition this route's
// 409 can mean (see handleTransitionStatusError) gets its own,
// distinct reason, not the same one.
func TestTransitionStatus_Handler_Conflict_Returns409WithReason(t *testing.T) {
	svc := &fakeService{
		transitionStatusFn: func(_, _ string, _ Status) (Task, error) {
			return Task{}, ErrConflict
		},
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.transitionStatus, http.MethodPatch, "/tasks/abc-123/status", `{"status":"done"}`)

	if w.Code != http.StatusConflict {
		t.Errorf("transitionStatus concurrency conflict status = %d, want %d", w.Code, http.StatusConflict)
	}

	var body errorResponse
	decodeBody(t, w, &body)
	if body.Reason != "concurrency" {
		t.Errorf(`transitionStatus concurrency conflict body reason = %q, want "concurrency"`, body.Reason)
	}
}

// TestUpdateTask_Handler_Conflict_HasNoReason proves the deliberate
// scope limit issue #153's own "Não faz" section states: PUT
// /tasks/{id}'s 409 has no transition-legality ambiguity to begin with,
// so it keeps its existing wire shape — no "reason" field — unlike
// PATCH /status's.
func TestUpdateTask_Handler_Conflict_HasNoReason(t *testing.T) {
	svc := &fakeService{
		updateTaskFn: func(_, _, _, _, _ string) (Task, error) { return Task{}, ErrConflict },
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.updateTask, http.MethodPut, "/tasks/abc-123", `{"title":"x"}`)

	if w.Code != http.StatusConflict {
		t.Errorf("updateTask conflict status = %d, want %d", w.Code, http.StatusConflict)
	}

	var raw map[string]any
	decodeBody(t, w, &raw)
	if _, present := raw["reason"]; present {
		t.Errorf(`updateTask conflict body has a "reason" field = %v, want it absent — PUT never got this issue's change`, raw["reason"])
	}
}

func TestTransitionStatus_Handler_InvalidJSON(t *testing.T) {
	h := newHandlerWithFake(&fakeService{})

	w := do(t, h.transitionStatus, http.MethodPatch, "/tasks/abc-123/status", `{invalid}`)

	if w.Code != http.StatusBadRequest {
		t.Errorf("transitionStatus invalid JSON status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// --- DELETE /tasks/{id} ---

func TestDeleteTask_Handler_ReturnsNoContent(t *testing.T) {
	h := newHandlerWithFake(&fakeService{})

	w := do(t, h.deleteTask, http.MethodDelete, "/tasks/abc-123", "")

	if w.Code != http.StatusNoContent {
		t.Errorf("deleteTask status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if w.Body.Len() != 0 {
		t.Errorf("deleteTask body must be empty, got %q", w.Body.String())
	}
}

// --- Error mapping ---

func TestHandler_UnexpectedError_Returns500(t *testing.T) {
	svc := &fakeService{
		getTaskFn: func(_, _ string) (Task, error) {
			return Task{}, errors.New("database exploded")
		},
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.getTask, http.MethodGet, "/tasks/abc-123", "")

	if w.Code != http.StatusInternalServerError {
		t.Errorf("unexpected error status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	var body map[string]string
	decodeBody(t, w, &body)
	if body["error"] != "internal server error" {
		t.Errorf("unexpected error body = %q, want %q", body["error"], "internal server error")
	}
	// Ensure internal details are not leaked.
	if strings.Contains(body["error"], "database") {
		t.Error("500 response must not expose internal error details")
	}
}

// --- Observability: what Handler itself logs ---

// TestHandler_UnexpectedError_LogsWithRequestIDMethodAndPath verifies that
// the one diagnostic log line Handler emits for an unmapped error carries
// enough context to correlate it with the access log for the same
// request (request_id) and to diagnose it (method, path, the real error).
func TestHandler_UnexpectedError_LogsWithRequestIDMethodAndPath(t *testing.T) {
	svc := &fakeService{
		getTaskFn: func(_, _ string) (Task, error) {
			return Task{}, errors.New("database exploded")
		},
	}
	h, buf := newHandlerWithFakeLogged(svc)

	// RequestID must run first so the Handler can read it back out, same
	// as in the real middleware chain built in cmd/api/main.go.
	handler := middleware.RequestID(http.HandlerFunc(h.getTask))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tasks/abc-123", nil)
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), testUserID))
	handler.ServeHTTP(w, req)

	wantRequestID := w.Header().Get(middleware.HeaderRequestID)
	if wantRequestID == "" {
		t.Fatal("test setup: response is missing X-Request-ID")
	}

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("failed to decode Handler log line %q: %v", buf.String(), err)
	}

	if entry["request_id"] != wantRequestID {
		t.Errorf("logged request_id = %v, want %v", entry["request_id"], wantRequestID)
	}
	if entry["method"] != http.MethodGet {
		t.Errorf("logged method = %v, want %v", entry["method"], http.MethodGet)
	}
	if entry["path"] != "/tasks/abc-123" {
		t.Errorf("logged path = %v, want %v", entry["path"], "/tasks/abc-123")
	}
	if got, _ := entry["error"].(string); !strings.Contains(got, "database exploded") {
		t.Errorf("logged error = %v, want it to contain the underlying error for diagnosis", entry["error"])
	}
}

// TestHandler_UnexpectedError_LogsExactlyOnce guards requirement: "Handler
// não deve registrar o mesmo erro várias vezes".
func TestHandler_UnexpectedError_LogsExactlyOnce(t *testing.T) {
	svc := &fakeService{
		getTaskFn: func(_, _ string) (Task, error) {
			return Task{}, errors.New("boom")
		},
	}
	h, buf := newHandlerWithFakeLogged(svc)

	w := do(t, h.getTask, http.MethodGet, "/tasks/abc-123", "")
	_ = w

	if got := strings.Count(buf.String(), "\n"); got != 1 {
		t.Errorf("Handler logged %d lines for a single failing request, want exactly 1: %q", got, buf.String())
	}
}

// TestHandler_ExpectedErrors_DoNotLog verifies that routine, mapped domain
// errors (404/400/409) are not logged by Handler at all — they are
// expected outcomes of client input, not failures needing a developer's
// attention, and the access log (Logging middleware) already records the
// resulting status for every request regardless.
func TestHandler_ExpectedErrors_DoNotLog(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"not found", ErrNotFound},
		{"invalid input", fmt.Errorf("%w: title must not be empty", ErrInvalidInput)},
		{"already exists", ErrAlreadyExists},
		{"invalid transition", fmt.Errorf("%w: cannot move from %q to %q", ErrInvalidTransition, StatusDone, StatusCancelled)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeService{
				getTaskFn: func(_, _ string) (Task, error) { return Task{}, tc.err },
			}
			h, buf := newHandlerWithFakeLogged(svc)

			do(t, h.getTask, http.MethodGet, "/tasks/abc-123", "")

			if buf.Len() != 0 {
				t.Errorf("Handler logged something for an expected/mapped error, want nothing: %q", buf.String())
			}
		})
	}
}

// --- Observability: the full strategy through the real middleware chain ---
//
// These two tests drive Handler through the same RequestID → Logging chain
// built in cmd/api/main.go, using two separate loggers (one for the access
// log, one for Handler's own diagnostic log) purely so each side's line
// count can be asserted independently — in production both are wired to
// the same *slog.Logger and end up interleaved in the same stream,
// correlated by request_id.

func TestLoggingStrategy_UnexpectedError_ProducesAccessLogAndDiagnosticLog(t *testing.T) {
	svc := &fakeService{
		getTaskFn: func(_, _ string) (Task, error) { return Task{}, errors.New("db exploded") },
	}
	h, handlerBuf := newHandlerWithFakeLogged(svc)

	var accessBuf bytes.Buffer
	accessLogger := slog.New(slog.NewJSONHandler(&accessBuf, nil))

	chained := middleware.Chain(
		middleware.RequestID,
		middleware.Logging(accessLogger),
	)(http.HandlerFunc(h.getTask))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tasks/abc-123", nil)
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), testUserID))
	chained.ServeHTTP(w, req)

	requestID := w.Header().Get(middleware.HeaderRequestID)

	if got := strings.Count(accessBuf.String(), "\n"); got != 1 {
		t.Fatalf("access log has %d lines, want exactly 1: %q", got, accessBuf.String())
	}
	if got := strings.Count(handlerBuf.String(), "\n"); got != 1 {
		t.Fatalf("Handler diagnostic log has %d lines, want exactly 1: %q", got, handlerBuf.String())
	}

	var access map[string]any
	if err := json.Unmarshal(accessBuf.Bytes(), &access); err != nil {
		t.Fatalf("decode access log: %v", err)
	}
	if access["level"] != "ERROR" {
		t.Errorf("access log level = %v, want ERROR", access["level"])
	}
	if access["request_id"] != requestID {
		t.Errorf("access log request_id = %v, want %v", access["request_id"], requestID)
	}

	var diag map[string]any
	if err := json.Unmarshal(handlerBuf.Bytes(), &diag); err != nil {
		t.Fatalf("decode diagnostic log: %v", err)
	}
	if diag["request_id"] != requestID {
		t.Errorf("diagnostic log request_id = %v, want %v (must correlate with the access log)", diag["request_id"], requestID)
	}
}

func TestLoggingStrategy_NotFound_ProducesOnlyAccessLog(t *testing.T) {
	svc := &fakeService{
		getTaskFn: func(_, _ string) (Task, error) { return Task{}, ErrNotFound },
	}
	h, handlerBuf := newHandlerWithFakeLogged(svc)

	var accessBuf bytes.Buffer
	accessLogger := slog.New(slog.NewJSONHandler(&accessBuf, nil))

	chained := middleware.Chain(
		middleware.RequestID,
		middleware.Logging(accessLogger),
	)(http.HandlerFunc(h.getTask))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tasks/abc-123", nil)
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), testUserID))
	chained.ServeHTTP(w, req)

	if got := strings.Count(accessBuf.String(), "\n"); got != 1 {
		t.Errorf("access log has %d lines, want exactly 1: %q", got, accessBuf.String())
	}
	if handlerBuf.Len() != 0 {
		t.Errorf("Handler logged something for an expected 404, want nothing: %q", handlerBuf.String())
	}

	var access map[string]any
	if err := json.Unmarshal(accessBuf.Bytes(), &access); err != nil {
		t.Fatalf("decode access log: %v", err)
	}
	if access["level"] != "WARN" {
		t.Errorf("access log level = %v, want WARN", access["level"])
	}
	if access["status"] != float64(http.StatusNotFound) {
		t.Errorf("access log status = %v, want %v", access["status"], http.StatusNotFound)
	}
}

// --- RegisterRoutes ---

func TestRegisterRoutes(t *testing.T) {
	svc := &fakeService{
		createTaskFn:       func(_, _, _, _ string) (Task, error) { return sampleTask(), nil },
		listTasksFn:        func(_ string, _, _ int, _, _ []string) ([]Task, error) { return []Task{sampleTask()}, nil },
		getTaskFn:          func(_, _ string) (Task, error) { return sampleTask(), nil },
		updateTaskFn:       func(_, _, _, _, _ string) (Task, error) { return sampleTask(), nil },
		completeTaskFn:     func(_, _ string) (Task, error) { return sampleTask(), nil },
		transitionStatusFn: func(_, _ string, _ Status) (Task, error) { return sampleTask(), nil },
		deleteTaskFn:       func(_, _ string) error { return nil },
	}
	h := newHandlerWithFake(svc)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, passthroughAuth)

	cases := []struct {
		method string
		path   string
		body   string
		want   int
	}{
		{http.MethodPost, "/tasks", `{"title":"T"}`, http.StatusCreated},
		{http.MethodGet, "/tasks", "", http.StatusOK},
		{http.MethodGet, "/tasks/abc-123", "", http.StatusOK},
		{http.MethodPut, "/tasks/abc-123", `{"title":"T"}`, http.StatusOK},
		{http.MethodPatch, "/tasks/abc-123/done", "", http.StatusOK},
		{http.MethodPatch, "/tasks/abc-123/status", `{"status":"in_progress"}`, http.StatusOK},
		{http.MethodDelete, "/tasks/abc-123", "", http.StatusNoContent},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("RegisterRoutes %s %s status = %d, want %d",
					tc.method, tc.path, w.Code, tc.want)
			}
		})
	}
}
