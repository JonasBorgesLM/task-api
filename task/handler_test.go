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

	"github.com/JonasBorgesLM/task-api/middleware"
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

func (f *fakeService) CreateTask(_ context.Context, title, description string) (Task, error) {
	f.createCalledWith = [2]string{title, description}
	if f.createTaskFn != nil {
		return f.createTaskFn(title, description)
	}
	return Task{}, nil
}

func (f *fakeService) GetTask(_ context.Context, id string) (Task, error) {
	if f.getTaskFn != nil {
		return f.getTaskFn(id)
	}
	return Task{}, nil
}

func (f *fakeService) ListTasks(_ context.Context) ([]Task, error) {
	if f.listTasksFn != nil {
		return f.listTasksFn()
	}
	return []Task{}, nil
}

func (f *fakeService) UpdateTask(_ context.Context, id, title, description string) (Task, error) {
	f.updateCalledWith = [3]string{id, title, description}
	if f.updateTaskFn != nil {
		return f.updateTaskFn(id, title, description)
	}
	return Task{}, nil
}

func (f *fakeService) DeleteTask(_ context.Context, id string) error {
	f.deleteCalledWith = id
	if f.deleteTaskFn != nil {
		return f.deleteTaskFn(id)
	}
	return nil
}

func (f *fakeService) CompleteTask(_ context.Context, id string) (Task, error) {
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

// newHandlerWithFakeLogged is like newHandlerWithFake, but wires a JSON
// logger that writes to the returned buffer instead of discarding output,
// for tests that need to inspect what Handler itself logged.
func newHandlerWithFakeLogged(svc *fakeService) (*Handler, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	return NewHandler(svc, logger), &buf
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

func TestListTasks_Handler_Pagination(t *testing.T) {
	all := []Task{
		{ID: "1"}, {ID: "2"}, {ID: "3"}, {ID: "4"}, {ID: "5"},
	}
	svc := &fakeService{
		listTasksFn: func() ([]Task, error) { return all, nil },
	}
	h := newHandlerWithFake(svc)

	cases := []struct {
		name    string
		query   string
		wantIDs []string
	}{
		{"no params returns everything", "", []string{"1", "2", "3", "4", "5"}},
		{"limit only", "?limit=2", []string{"1", "2"}},
		{"offset only", "?offset=3", []string{"4", "5"}},
		{"limit and offset", "?limit=2&offset=1", []string{"2", "3"}},
		{"limit beyond end", "?limit=100", []string{"1", "2", "3", "4", "5"}},
		{"offset beyond end", "?offset=100", []string{}},
		{"limit zero", "?limit=0", []string{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := do(t, h.listTasks, http.MethodGet, "/tasks"+tc.query, "")

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
			}

			var got []Task
			decodeBody(t, w, &got)

			gotIDs := make([]string, len(got))
			for i, task := range got {
				gotIDs[i] = task.ID
			}
			if len(gotIDs) != len(tc.wantIDs) {
				t.Fatalf("IDs = %v, want %v", gotIDs, tc.wantIDs)
			}
			for i := range tc.wantIDs {
				if gotIDs[i] != tc.wantIDs[i] {
					t.Errorf("IDs = %v, want %v", gotIDs, tc.wantIDs)
				}
			}
		})
	}
}

func TestListTasks_Handler_InvalidPaginationParams(t *testing.T) {
	svc := &fakeService{
		listTasksFn: func() ([]Task, error) { return []Task{{ID: "1"}}, nil },
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

// --- Observability: what Handler itself logs ---

// TestHandler_UnexpectedError_LogsWithRequestIDMethodAndPath verifies that
// the one diagnostic log line Handler emits for an unmapped error carries
// enough context to correlate it with the access log for the same
// request (request_id) and to diagnose it (method, path, the real error).
func TestHandler_UnexpectedError_LogsWithRequestIDMethodAndPath(t *testing.T) {
	svc := &fakeService{
		getTaskFn: func(_ string) (Task, error) {
			return Task{}, errors.New("database exploded")
		},
	}
	h, buf := newHandlerWithFakeLogged(svc)

	// RequestID must run first so the Handler can read it back out, same
	// as in the real middleware chain built in cmd/api/main.go.
	handler := middleware.RequestID(http.HandlerFunc(h.getTask))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tasks/abc-123", nil))

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
		getTaskFn: func(_ string) (Task, error) {
			return Task{}, errors.New("boom")
		},
	}
	h, buf := newHandlerWithFakeLogged(svc)

	w := httptest.NewRecorder()
	h.getTask(w, httptest.NewRequest(http.MethodGet, "/tasks/abc-123", nil))

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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeService{
				getTaskFn: func(_ string) (Task, error) { return Task{}, tc.err },
			}
			h, buf := newHandlerWithFakeLogged(svc)

			w := httptest.NewRecorder()
			h.getTask(w, httptest.NewRequest(http.MethodGet, "/tasks/abc-123", nil))

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
		getTaskFn: func(_ string) (Task, error) { return Task{}, errors.New("db exploded") },
	}
	h, handlerBuf := newHandlerWithFakeLogged(svc)

	var accessBuf bytes.Buffer
	accessLogger := slog.New(slog.NewJSONHandler(&accessBuf, nil))

	chained := middleware.Chain(
		middleware.RequestID,
		middleware.Logging(accessLogger),
	)(http.HandlerFunc(h.getTask))

	w := httptest.NewRecorder()
	chained.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tasks/abc-123", nil))

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
		getTaskFn: func(_ string) (Task, error) { return Task{}, ErrNotFound },
	}
	h, handlerBuf := newHandlerWithFakeLogged(svc)

	var accessBuf bytes.Buffer
	accessLogger := slog.New(slog.NewJSONHandler(&accessBuf, nil))

	chained := middleware.Chain(
		middleware.RequestID,
		middleware.Logging(accessLogger),
	)(http.HandlerFunc(h.getTask))

	w := httptest.NewRecorder()
	chained.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tasks/abc-123", nil))

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
