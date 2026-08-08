package task

import (
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
)

// fakeService is a test double for taskService.
type fakeService struct {
	createTaskFn   func(title, description string) (Task, error)
	getTaskFn      func(id string) (Task, error)
	listTasksFn    func() ([]Task, error)
	updateTaskFn   func(id, title, description string) (Task, error)
	deleteTaskFn   func(id string) error
	completeTaskFn func(id string) (Task, error)

	// Call recording.
	createCalledWith   [2]string // [title, description]
	updateCalledWith   [3]string // [id, title, description]
	completeCalledWith string
	deleteCalledWith   string
}

func (f *fakeService) CreateTask(title, description string) (Task, error) {
	f.createCalledWith = [2]string{title, description}
	if f.createTaskFn != nil {
		return f.createTaskFn(title, description)
	}
	return Task{}, nil
}

func (f *fakeService) GetTask(id string) (Task, error) {
	if f.getTaskFn != nil {
		return f.getTaskFn(id)
	}
	return Task{}, nil
}

func (f *fakeService) ListTasks() ([]Task, error) {
	if f.listTasksFn != nil {
		return f.listTasksFn()
	}
	return []Task{}, nil
}

func (f *fakeService) UpdateTask(id, title, description string) (Task, error) {
	f.updateCalledWith = [3]string{id, title, description}
	if f.updateTaskFn != nil {
		return f.updateTaskFn(id, title, description)
	}
	return Task{}, nil
}

func (f *fakeService) DeleteTask(id string) error {
	f.deleteCalledWith = id
	if f.deleteTaskFn != nil {
		return f.deleteTaskFn(id)
	}
	return nil
}

func (f *fakeService) CompleteTask(id string) (Task, error) {
	f.completeCalledWith = id
	if f.completeTaskFn != nil {
		return f.completeTaskFn(id)
	}
	return Task{}, nil
}

// sampleTask returns a Task with predictable values for use in handler tests.
func sampleTask() Task {
	now := time.Now()
	return Task{
		ID:          "abc-123",
		Title:       "Sample task",
		Description: "Sample description",
		Status:      StatusPending,
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

// do executes an HTTP request against the given handler function
// and returns the recorded response.
func do(t *testing.T, handler http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
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
		createTaskFn: func(title, description string) (Task, error) {
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
		createTaskFn: func(_, _ string) (Task, error) {
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
		createTaskFn: func(title, description string) (Task, error) {
			return Task{Title: title, Description: description, Status: StatusPending}, nil
		},
	}
	h := newHandlerWithFake(svc)

	// Client attempts to inject protected fields.
	w := do(t, h.createTask, http.MethodPost, "/tasks",
		`{"title":"T","description":"D","id":"injected","status":"done","created_at":"2000-01-01T00:00:00Z"}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("createTask status = %d, want %d", w.Code, http.StatusCreated)
	}

	// The service was called only with the allowed fields.
	if svc.createCalledWith[0] != "T" {
		t.Errorf("createTask title sent to service = %q, want %q", svc.createCalledWith[0], "T")
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
		listTasksFn: func() ([]Task, error) { return tasks, nil },
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
		listTasksFn: func() ([]Task, error) { return nil, nil },
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

// --- GET /tasks/{id} ---

func TestGetTask_Handler_Found(t *testing.T) {
	task := sampleTask()
	svc := &fakeService{
		getTaskFn: func(id string) (Task, error) { return task, nil },
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
		getTaskFn: func(_ string) (Task, error) { return Task{}, ErrNotFound },
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
		updateTaskFn: func(id, title, description string) (Task, error) { return task, nil },
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
		updateTaskFn: func(id, title, description string) (Task, error) {
			return Task{ID: id, Title: title, Description: description, Status: StatusPending}, nil
		},
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.updateTask, http.MethodPut, "/tasks/abc-123",
		`{"title":"T","description":"D","status":"done","id":"injected"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("updateTask status = %d, want %d", w.Code, http.StatusOK)
	}

	// Service was called with the correct fields only.
	if svc.updateCalledWith[1] != "T" {
		t.Errorf("updateTask title sent to service = %q, want %q", svc.updateCalledWith[1], "T")
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
		completeTaskFn: func(id string) (Task, error) { return task, nil },
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
		getTaskFn: func(_ string) (Task, error) {
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

// --- RegisterRoutes ---

func TestRegisterRoutes(t *testing.T) {
	svc := &fakeService{
		createTaskFn:   func(_, _ string) (Task, error) { return sampleTask(), nil },
		listTasksFn:    func() ([]Task, error) { return []Task{sampleTask()}, nil },
		getTaskFn:      func(_ string) (Task, error) { return sampleTask(), nil },
		updateTaskFn:   func(_, _, _ string) (Task, error) { return sampleTask(), nil },
		completeTaskFn: func(_ string) (Task, error) { return sampleTask(), nil },
		deleteTaskFn:   func(_ string) error { return nil },
	}
	h := newHandlerWithFake(svc)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

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
