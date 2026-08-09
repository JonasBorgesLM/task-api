package task

// Full-stack HTTP integration tests: real MemoryRepository, real Service,
// real Handler, real http.ServeMux, driven entirely through httptest — no
// fakeService or fakeRepository anywhere in this file.
//
// These tests intentionally do NOT re-derive what handler_test.go (fake
// Service) and service_test.go (fake Repository) already cover in
// isolation — e.g. whitespace-only titles, duplicate IDs, or every error
// message string. What they add is coverage those unit tests structurally
// cannot provide:
//
//   - that Handler, Service and MemoryRepository are wired correctly end
//     to end (a mistake at either boundary wouldn't be caught by either
//     unit suite alone, since each mocks the other side of the boundary);
//   - that data written by one HTTP request is actually visible to a
//     later, separate HTTP request against the same server (real
//     persistence — fakes have no state to lose);
//   - the exact wire format (HTTP status, Content-Type, JSON key casing)
//     seen by a real client, encoded and decoded for real.

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newIntegrationServer wires a real MemoryRepository, Service and Handler
// together — the same composition cmd/api/main.go uses — and returns an
// httptest.Server backed by a real http.ServeMux. Each call gets its own
// fresh in-memory store, so tests never share state with one another.
func newIntegrationServer(t *testing.T) *httptest.Server {
	t.Helper()

	repo := NewMemoryRepository()
	svc := NewService(repo)
	handler := NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

// doRequest issues a real HTTP request against srv and returns the
// response. The caller does not need to close the body; it's closed via
// t.Cleanup.
func doRequest(t *testing.T, srv *httptest.Server, method, path, body string) *http.Response {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatalf("build request %s %s: %v", method, path, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	return resp
}

func decodeTask(t *testing.T, resp *http.Response) Task {
	t.Helper()
	var got Task
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode Task response: %v", err)
	}
	return got
}

func decodeTasks(t *testing.T, resp *http.Response) []Task {
	t.Helper()
	var got []Task
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode []Task response: %v", err)
	}
	return got
}

func assertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Errorf("%s %s status = %d, want %d", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, want)
	}
}

func assertJSONContentType(t *testing.T, resp *http.Response) {
	t.Helper()
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("%s %s Content-Type = %q, want %q", resp.Request.Method, resp.Request.URL.Path, ct, "application/json")
	}
}

// --- POST /tasks ---

// TestIntegration_CreateTask_Valid also pins down the exact wire format:
// snake_case JSON keys, matching Task's json tags. A decode-only assertion
// (as used elsewhere) would not catch a broken or missing json tag, since
// encoding/json falls back to case-insensitive field matching on decode.
func TestIntegration_CreateTask_Valid(t *testing.T) {
	srv := newIntegrationServer(t)

	resp := doRequest(t, srv, http.MethodPost, "/tasks", `{"title":"Buy groceries","description":"Milk and eggs"}`)

	assertStatus(t, resp, http.StatusCreated)
	assertJSONContentType(t, resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode raw JSON: %v", err)
	}
	for _, key := range []string{"id", "title", "description", "status", "created_at", "updated_at"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("response JSON is missing expected snake_case key %q; got keys %v", key, rawKeys(raw))
		}
	}

	var got Task
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode Task: %v", err)
	}
	if got.ID == "" {
		t.Error("ID must not be empty")
	}
	if got.Title != "Buy groceries" {
		t.Errorf("Title = %q, want %q", got.Title, "Buy groceries")
	}
	if got.Description != "Milk and eggs" {
		t.Errorf("Description = %q, want %q", got.Description, "Milk and eggs")
	}
	if got.Status != StatusPending {
		t.Errorf("Status = %q, want %q", got.Status, StatusPending)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("CreatedAt/UpdatedAt must be set")
	}
}

func rawKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestIntegration_CreateTask_InvalidJSON(t *testing.T) {
	srv := newIntegrationServer(t)

	resp := doRequest(t, srv, http.MethodPost, "/tasks", `{invalid`)

	assertStatus(t, resp, http.StatusBadRequest)
	assertJSONContentType(t, resp)
}

func TestIntegration_CreateTask_EmptyTitle(t *testing.T) {
	srv := newIntegrationServer(t)

	resp := doRequest(t, srv, http.MethodPost, "/tasks", `{"title":""}`)

	assertStatus(t, resp, http.StatusBadRequest)
	assertJSONContentType(t, resp)
}

// --- GET /tasks ---

func TestIntegration_ListTasks_Empty(t *testing.T) {
	srv := newIntegrationServer(t)

	resp := doRequest(t, srv, http.MethodGet, "/tasks", "")

	assertStatus(t, resp, http.StatusOK)
	assertJSONContentType(t, resp)

	if got := decodeTasks(t, resp); len(got) != 0 {
		t.Errorf("len(tasks) = %d, want 0", len(got))
	}
}

func TestIntegration_ListTasks_WithData(t *testing.T) {
	srv := newIntegrationServer(t)

	titles := []string{"Task A", "Task B", "Task C"}
	for _, title := range titles {
		resp := doRequest(t, srv, http.MethodPost, "/tasks", `{"title":"`+title+`"}`)
		assertStatus(t, resp, http.StatusCreated)
	}

	resp := doRequest(t, srv, http.MethodGet, "/tasks", "")
	assertStatus(t, resp, http.StatusOK)

	got := decodeTasks(t, resp)
	if len(got) != len(titles) {
		t.Fatalf("len(tasks) = %d, want %d", len(got), len(titles))
	}

	seen := make(map[string]bool, len(got))
	for _, task := range got {
		seen[task.Title] = true
	}
	for _, title := range titles {
		if !seen[title] {
			t.Errorf("GET /tasks is missing a task titled %q", title)
		}
	}
}

// --- GET /tasks/{id} ---

func TestIntegration_GetTask_NotFound(t *testing.T) {
	srv := newIntegrationServer(t)

	resp := doRequest(t, srv, http.MethodGet, "/tasks/does-not-exist", "")

	assertStatus(t, resp, http.StatusNotFound)
	assertJSONContentType(t, resp)
}

// --- PUT /tasks/{id} ---

// TestIntegration_UpdateTask verifies the update is visible not just in
// the PUT response, but in a completely separate, later GET request —
// proving it was actually persisted in the shared MemoryRepository rather
// than just echoed back.
func TestIntegration_UpdateTask(t *testing.T) {
	srv := newIntegrationServer(t)

	created := decodeTask(t, doRequest(t, srv, http.MethodPost, "/tasks",
		`{"title":"Original","description":"Original desc"}`))

	updateResp := doRequest(t, srv, http.MethodPut, "/tasks/"+created.ID,
		`{"title":"Updated","description":"Updated desc"}`)
	assertStatus(t, updateResp, http.StatusOK)
	assertJSONContentType(t, updateResp)

	updated := decodeTask(t, updateResp)
	if updated.Title != "Updated" || updated.Description != "Updated desc" {
		t.Errorf("PUT response Title/Description = %q/%q, want %q/%q",
			updated.Title, updated.Description, "Updated", "Updated desc")
	}
	if updated.ID != created.ID {
		t.Errorf("PUT changed the task ID: got %q, want %q", updated.ID, created.ID)
	}
	if !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("PUT changed CreatedAt: got %v, want %v", updated.CreatedAt, created.CreatedAt)
	}

	// Persistence: a fresh, separate GET must reflect the update.
	persisted := decodeTask(t, doRequest(t, srv, http.MethodGet, "/tasks/"+created.ID, ""))
	if persisted.Title != "Updated" || persisted.Description != "Updated desc" {
		t.Errorf("update did not persist: GET returned Title/Description = %q/%q",
			persisted.Title, persisted.Description)
	}
}

func TestIntegration_UpdateTask_NotFound(t *testing.T) {
	srv := newIntegrationServer(t)

	resp := doRequest(t, srv, http.MethodPut, "/tasks/does-not-exist", `{"title":"Title"}`)

	assertStatus(t, resp, http.StatusNotFound)
}

// --- PATCH /tasks/{id}/done ---

// TestIntegration_CompleteTask_Idempotent covers both "conclusão" and
// "conclusão idempotente" against the real stack: calling PATCH twice
// must leave UpdatedAt unchanged on the second call, and the completion
// must be visible in a later, separate GET.
func TestIntegration_CompleteTask_Idempotent(t *testing.T) {
	srv := newIntegrationServer(t)

	created := decodeTask(t, doRequest(t, srv, http.MethodPost, "/tasks", `{"title":"To finish"}`))

	firstResp := doRequest(t, srv, http.MethodPatch, "/tasks/"+created.ID+"/done", "")
	assertStatus(t, firstResp, http.StatusOK)
	assertJSONContentType(t, firstResp)

	first := decodeTask(t, firstResp)
	if first.Status != StatusDone {
		t.Fatalf("first PATCH Status = %q, want %q", first.Status, StatusDone)
	}

	secondResp := doRequest(t, srv, http.MethodPatch, "/tasks/"+created.ID+"/done", "")
	assertStatus(t, secondResp, http.StatusOK)

	second := decodeTask(t, secondResp)
	if second.Status != StatusDone {
		t.Errorf("second PATCH Status = %q, want %q", second.Status, StatusDone)
	}
	if !second.UpdatedAt.Equal(first.UpdatedAt) {
		t.Errorf("second PATCH changed UpdatedAt: got %v, want %v (must be idempotent)",
			second.UpdatedAt, first.UpdatedAt)
	}

	// Persistence: a fresh, separate GET must still show it as done.
	persisted := decodeTask(t, doRequest(t, srv, http.MethodGet, "/tasks/"+created.ID, ""))
	if persisted.Status != StatusDone {
		t.Errorf("completion did not persist: GET returned Status = %q", persisted.Status)
	}
}

func TestIntegration_CompleteTask_NotFound(t *testing.T) {
	srv := newIntegrationServer(t)

	resp := doRequest(t, srv, http.MethodPatch, "/tasks/does-not-exist/done", "")

	assertStatus(t, resp, http.StatusNotFound)
}

// --- DELETE /tasks/{id} ---

// TestIntegration_DeleteTask verifies deletion is durable: gone from a
// later GET by ID, and gone from a later GET /tasks listing too.
func TestIntegration_DeleteTask(t *testing.T) {
	srv := newIntegrationServer(t)

	created := decodeTask(t, doRequest(t, srv, http.MethodPost, "/tasks", `{"title":"To delete"}`))

	deleteResp := doRequest(t, srv, http.MethodDelete, "/tasks/"+created.ID, "")
	assertStatus(t, deleteResp, http.StatusNoContent)

	remainingBody, err := io.ReadAll(deleteResp.Body)
	if err != nil {
		t.Fatalf("read DELETE body: %v", err)
	}
	if len(remainingBody) != 0 {
		t.Errorf("DELETE response body = %q, want empty", remainingBody)
	}

	// Persistence: a fresh, separate GET must now report it as gone.
	getResp := doRequest(t, srv, http.MethodGet, "/tasks/"+created.ID, "")
	assertStatus(t, getResp, http.StatusNotFound)

	// Persistence: it must also be gone from the listing.
	listResp := doRequest(t, srv, http.MethodGet, "/tasks", "")
	for _, task := range decodeTasks(t, listResp) {
		if task.ID == created.ID {
			t.Errorf("deleted task %q is still present in GET /tasks", created.ID)
		}
	}
}

func TestIntegration_DeleteTask_NotFound(t *testing.T) {
	srv := newIntegrationServer(t)

	resp := doRequest(t, srv, http.MethodDelete, "/tasks/does-not-exist", "")

	assertStatus(t, resp, http.StatusNotFound)
}
