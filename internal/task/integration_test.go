package task

// Full-stack HTTP integration tests: real MemoryRepository (task and
// user), real Service, real Handler, real http.ServeMux — including the
// real auth flow (POST /auth/register, POST /auth/login, and
// user.RequireAuth) — driven entirely through httptest. No fakeService or
// fakeRepository anywhere in this file.
//
// These tests intentionally do NOT re-derive what handler_test.go (fake
// Service) and service_test.go (fake Repository) already cover in
// isolation — e.g. whitespace-only titles, duplicate IDs, or every error
// message string. What they add is coverage those unit tests structurally
// cannot provide:
//
//   - that task.Handler, task.Service, task.MemoryRepository, and the
//     user package's auth flow are all wired correctly end to end (a
//     mistake at any boundary wouldn't be caught by a unit suite that
//     mocks the other side of that boundary);
//   - that data written by one HTTP request is actually visible to a
//     later, separate HTTP request against the same server (real
//     persistence — fakes have no state to lose);
//   - the exact wire format (HTTP status, Content-Type, JSON key casing)
//     seen by a real client, encoded and decoded for real;
//   - that requireAuth actually gates every task route, and that
//     ownership scoping actually isolates one user's tasks from another's
//     — see TestIntegration_RequiresAuthentication and
//     TestIntegration_OwnershipIsolation.

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JonasBorgesLM/task-api/internal/user"
)

// newIntegrationServer wires real MemoryRepositorys (task and user), real
// Services, real Handlers and user.RequireAuth together — the same
// composition cmd/api/main.go uses — and returns an httptest.Server
// backed by a real http.ServeMux, plus a bearer token for one already
// registered-and-logged-in user ("owner@example.com"). Each call gets its
// own fresh in-memory stores, so tests never share state with one
// another.
func newIntegrationServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	taskSvc := NewService(NewMemoryRepository())
	taskHandler := NewHandler(taskSvc, logger)

	userSvc := user.NewService(user.NewMemoryRepository(), 24*time.Hour, 1000)
	userHandler := user.NewHandler(userSvc, logger)
	requireAuth := user.RequireAuth(userSvc, logger)

	// A generous limit here: these tests exercise the task/auth stack, not
	// RateLimiter itself (see internal/middleware/rate_limit_test.go for
	// that) — several tests here call registerAndLogin more than once
	// (see TestIntegration_OwnershipIsolation), and a real, tight limit
	// would make those flaky for reasons unrelated to what they check.
	// A pass-through: these tests cover routing and auth, not rate
	// limiting, and the limiter that guards these routes in production is
	// composed in cmd/api (see newServer) rather than here.
	noopRateLimit := func(next http.Handler) http.Handler { return next }

	mux := http.NewServeMux()
	userHandler.RegisterRoutes(mux, requireAuth, noopRateLimit)
	taskHandler.RegisterRoutes(mux, requireAuth)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	token := registerAndLogin(t, srv, "owner@example.com")

	return srv, token
}

// registerAndLogin drives the real POST /auth/register and POST
// /auth/login endpoints for email, and returns the bearer token login
// issued.
func registerAndLogin(t *testing.T, srv *httptest.Server, email string) string {
	t.Helper()

	body := fmt.Sprintf(`{"email":%q,"password":"password12345"}`, email)

	regResp := doRequest(t, srv, "", http.MethodPost, "/auth/register", body)
	if regResp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(regResp.Body)
		t.Fatalf("register %s: status = %d, body = %s", email, regResp.StatusCode, respBody)
	}

	loginResp := doRequest(t, srv, "", http.MethodPost, "/auth/login", body)
	if loginResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("login %s: status = %d, body = %s", email, loginResp.StatusCode, respBody)
	}

	var loginBody struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&loginBody); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginBody.Token == "" {
		t.Fatal("login response missing token")
	}

	return loginBody.Token
}

// doRequest issues a real HTTP request against srv and returns the
// response. If token is non-empty, it's sent as "Authorization: Bearer
// <token>". The caller does not need to close the body; it's closed via
// t.Cleanup.
func doRequest(t *testing.T, srv *httptest.Server, token, method, path, body string) *http.Response {
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
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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

// --- Authentication gating ---

// TestIntegration_RequiresAuthentication verifies that every task route is
// actually behind requireAuth — a request with no Authorization header at
// all must never reach task.Handler.
func TestIntegration_RequiresAuthentication(t *testing.T) {
	srv, _ := newIntegrationServer(t)

	resp := doRequest(t, srv, "", http.MethodGet, "/tasks", "")

	assertStatus(t, resp, http.StatusUnauthorized)
}

// TestIntegration_Logout_InvalidatesToken drives the real POST /auth/logout
// endpoint — through the real user.RequireAuth middleware and real
// user.Service, not a fake — and confirms the token it invalidated no
// longer authenticates any subsequent request.
func TestIntegration_Logout_InvalidatesToken(t *testing.T) {
	srv, token := newIntegrationServer(t)

	logoutResp := doRequest(t, srv, token, http.MethodPost, "/auth/logout", "")
	assertStatus(t, logoutResp, http.StatusNoContent)

	afterResp := doRequest(t, srv, token, http.MethodGet, "/tasks", "")
	assertStatus(t, afterResp, http.StatusUnauthorized)
}

// TestIntegration_Logout_Twice_SecondCallIsUnauthorized verifies that
// /auth/logout itself, not just /tasks, correctly rejects a token that
// logout has already invalidated — a repeated logout with the same token
// is a 401 (RequireAuth rejects it before the request ever reaches
// Service.Logout), not a special "already logged out" success case.
func TestIntegration_Logout_Twice_SecondCallIsUnauthorized(t *testing.T) {
	srv, token := newIntegrationServer(t)

	first := doRequest(t, srv, token, http.MethodPost, "/auth/logout", "")
	assertStatus(t, first, http.StatusNoContent)

	second := doRequest(t, srv, token, http.MethodPost, "/auth/logout", "")
	assertStatus(t, second, http.StatusUnauthorized)
}

// TestIntegration_OwnershipIsolation verifies that a task created by one
// user is invisible to another authenticated user — reported as 404, not
// present in their listing, and not deletable by them — end to end
// through the real auth flow and real ownership-scoped Repository
// queries.
func TestIntegration_OwnershipIsolation(t *testing.T) {
	srv, ownerToken := newIntegrationServer(t)
	otherToken := registerAndLogin(t, srv, "someone-else@example.com")

	created := decodeTask(t, doRequest(t, srv, ownerToken, http.MethodPost, "/tasks", `{"title":"Owner's task"}`))

	getResp := doRequest(t, srv, otherToken, http.MethodGet, "/tasks/"+created.ID, "")
	assertStatus(t, getResp, http.StatusNotFound)

	deleteResp := doRequest(t, srv, otherToken, http.MethodDelete, "/tasks/"+created.ID, "")
	assertStatus(t, deleteResp, http.StatusNotFound)

	listResp := doRequest(t, srv, otherToken, http.MethodGet, "/tasks", "")
	assertStatus(t, listResp, http.StatusOK)
	for _, task := range decodeTasks(t, listResp) {
		if task.ID == created.ID {
			t.Errorf("GET /tasks for a different user returned another user's task %q", created.ID)
		}
	}

	// The owner can still see it.
	ownerGet := doRequest(t, srv, ownerToken, http.MethodGet, "/tasks/"+created.ID, "")
	assertStatus(t, ownerGet, http.StatusOK)
}

// TestIntegration_OwnershipIsolation_WriteRoutes covers the write verbs
// that TestIntegration_OwnershipIsolation does not: PUT and both PATCH
// routes. They are the ones where a missing ownership check is worst —
// a read leak exposes a task, a write leak lets a stranger mutate one —
// and PATCH /tasks/{id}/status in particular was added after the
// ownership rules were written.
//
// The expected answer is 404, not 403: a non-owner must not be able to
// tell an ID that exists from one that does not (see the Repository's
// doc comment).
func TestIntegration_OwnershipIsolation_WriteRoutes(t *testing.T) {
	tests := []struct {
		name   string
		method string
		suffix string
		body   string
	}{
		{"update", http.MethodPut, "", `{"title":"Hijacked","description":"by a stranger"}`},
		{"complete", http.MethodPatch, "/done", ""},
		{"transition status", http.MethodPatch, "/status", `{"status":"cancelled"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, ownerToken := newIntegrationServer(t)
			otherToken := registerAndLogin(t, srv, "someone-else@example.com")

			created := decodeTask(t, doRequest(t, srv, ownerToken, http.MethodPost, "/tasks",
				`{"title":"Owner's task","description":"untouched"}`))

			resp := doRequest(t, srv, otherToken, tt.method, "/tasks/"+created.ID+tt.suffix, tt.body)
			assertStatus(t, resp, http.StatusNotFound)

			// A rejected write must also be a write that did not happen:
			// a handler that returns 404 after having already persisted
			// the change would pass the assertion above and still have
			// let a stranger mutate the task.
			after := decodeTask(t, doRequest(t, srv, ownerToken, http.MethodGet, "/tasks/"+created.ID, ""))
			if after.Title != created.Title {
				t.Errorf("title = %q after a rejected cross-user %s, want it unchanged (%q)", after.Title, tt.method, created.Title)
			}
			if after.Description != created.Description {
				t.Errorf("description = %q after a rejected cross-user %s, want it unchanged (%q)", after.Description, tt.method, created.Description)
			}
			if after.Status != created.Status {
				t.Errorf("status = %q after a rejected cross-user %s, want it unchanged (%q)", after.Status, tt.method, created.Status)
			}
			if after.UpdatedAt != created.UpdatedAt {
				t.Errorf("updated_at moved after a rejected cross-user %s — something was written", tt.method)
			}
		})
	}
}

// --- POST /tasks ---

// TestIntegration_CreateTask_Valid also pins down the exact wire format:
// snake_case JSON keys, matching Task's json tags. A decode-only assertion
// (as used elsewhere) would not catch a broken or missing json tag, since
// encoding/json falls back to case-insensitive field matching on decode.
func TestIntegration_CreateTask_Valid(t *testing.T) {
	srv, token := newIntegrationServer(t)

	resp := doRequest(t, srv, token, http.MethodPost, "/tasks", `{"title":"Buy groceries","description":"Milk and eggs"}`)

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
	for _, key := range []string{"id", "title", "description", "status", "priority", "created_at", "updated_at"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("response JSON is missing expected snake_case key %q; got keys %v", key, rawKeys(raw))
		}
	}
	if _, ok := raw["user_id"]; ok {
		t.Error("response JSON must not expose user_id (see Task.UserID's json:\"-\")")
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
	if got.Priority != PriorityMedium {
		t.Errorf("Priority = %q, want default %q", got.Priority, PriorityMedium)
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
	srv, token := newIntegrationServer(t)

	resp := doRequest(t, srv, token, http.MethodPost, "/tasks", `{invalid`)

	assertStatus(t, resp, http.StatusBadRequest)
	assertJSONContentType(t, resp)
}

func TestIntegration_CreateTask_EmptyTitle(t *testing.T) {
	srv, token := newIntegrationServer(t)

	resp := doRequest(t, srv, token, http.MethodPost, "/tasks", `{"title":""}`)

	assertStatus(t, resp, http.StatusBadRequest)
	assertJSONContentType(t, resp)
}

// --- GET /tasks ---

func TestIntegration_ListTasks_Empty(t *testing.T) {
	srv, token := newIntegrationServer(t)

	resp := doRequest(t, srv, token, http.MethodGet, "/tasks", "")

	assertStatus(t, resp, http.StatusOK)
	assertJSONContentType(t, resp)

	if got := decodeTasks(t, resp); len(got) != 0 {
		t.Errorf("len(tasks) = %d, want 0", len(got))
	}
}

func TestIntegration_ListTasks_WithData(t *testing.T) {
	srv, token := newIntegrationServer(t)

	titles := []string{"Task A", "Task B", "Task C"}
	for _, title := range titles {
		resp := doRequest(t, srv, token, http.MethodPost, "/tasks", `{"title":"`+title+`"}`)
		assertStatus(t, resp, http.StatusCreated)
	}

	resp := doRequest(t, srv, token, http.MethodGet, "/tasks", "")
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

// TestIntegration_ListTasks_Pagination drives the real stack through
// limit/offset over tasks created (and therefore CreatedAt-ordered) in a
// known sequence, confirming pagination and the underlying deterministic
// ordering work together correctly end to end.
func TestIntegration_ListTasks_Pagination(t *testing.T) {
	srv, token := newIntegrationServer(t)

	titles := []string{"A", "B", "C", "D", "E"}
	for _, title := range titles {
		resp := doRequest(t, srv, token, http.MethodPost, "/tasks", `{"title":"`+title+`"}`)
		assertStatus(t, resp, http.StatusCreated)
	}

	resp := doRequest(t, srv, token, http.MethodGet, "/tasks?limit=2&offset=1", "")
	assertStatus(t, resp, http.StatusOK)

	got := decodeTasks(t, resp)
	if len(got) != 2 {
		t.Fatalf("len(tasks) = %d, want 2", len(got))
	}
	if got[0].Title != "B" || got[1].Title != "C" {
		t.Errorf("titles = [%q, %q], want [%q, %q]", got[0].Title, got[1].Title, "B", "C")
	}
}

// --- GET /tasks/{id} ---

func TestIntegration_GetTask_NotFound(t *testing.T) {
	srv, token := newIntegrationServer(t)

	resp := doRequest(t, srv, token, http.MethodGet, "/tasks/does-not-exist", "")

	assertStatus(t, resp, http.StatusNotFound)
	assertJSONContentType(t, resp)
}

// --- PUT /tasks/{id} ---

// TestIntegration_UpdateTask verifies the update is visible not just in
// the PUT response, but in a completely separate, later GET request —
// proving it was actually persisted in the shared MemoryRepository rather
// than just echoed back.
func TestIntegration_UpdateTask(t *testing.T) {
	srv, token := newIntegrationServer(t)

	created := decodeTask(t, doRequest(t, srv, token, http.MethodPost, "/tasks",
		`{"title":"Original","description":"Original desc"}`))

	updateResp := doRequest(t, srv, token, http.MethodPut, "/tasks/"+created.ID,
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
	persisted := decodeTask(t, doRequest(t, srv, token, http.MethodGet, "/tasks/"+created.ID, ""))
	if persisted.Title != "Updated" || persisted.Description != "Updated desc" {
		t.Errorf("update did not persist: GET returned Title/Description = %q/%q",
			persisted.Title, persisted.Description)
	}
}

func TestIntegration_UpdateTask_NotFound(t *testing.T) {
	srv, token := newIntegrationServer(t)

	resp := doRequest(t, srv, token, http.MethodPut, "/tasks/does-not-exist", `{"title":"Title"}`)

	assertStatus(t, resp, http.StatusNotFound)
}

// --- PATCH /tasks/{id}/done ---

// TestIntegration_CompleteTask_Idempotent covers both "conclusão" and
// "conclusão idempotente" against the real stack: calling PATCH twice
// must leave UpdatedAt unchanged on the second call, and the completion
// must be visible in a later, separate GET.
func TestIntegration_CompleteTask_Idempotent(t *testing.T) {
	srv, token := newIntegrationServer(t)

	created := decodeTask(t, doRequest(t, srv, token, http.MethodPost, "/tasks", `{"title":"To finish"}`))

	firstResp := doRequest(t, srv, token, http.MethodPatch, "/tasks/"+created.ID+"/done", "")
	assertStatus(t, firstResp, http.StatusOK)
	assertJSONContentType(t, firstResp)

	first := decodeTask(t, firstResp)
	if first.Status != StatusDone {
		t.Fatalf("first PATCH Status = %q, want %q", first.Status, StatusDone)
	}

	secondResp := doRequest(t, srv, token, http.MethodPatch, "/tasks/"+created.ID+"/done", "")
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
	persisted := decodeTask(t, doRequest(t, srv, token, http.MethodGet, "/tasks/"+created.ID, ""))
	if persisted.Status != StatusDone {
		t.Errorf("completion did not persist: GET returned Status = %q", persisted.Status)
	}
}

func TestIntegration_CompleteTask_NotFound(t *testing.T) {
	srv, token := newIntegrationServer(t)

	resp := doRequest(t, srv, token, http.MethodPatch, "/tasks/does-not-exist/done", "")

	assertStatus(t, resp, http.StatusNotFound)
}

// --- PATCH /tasks/{id}/status ---

// TestIntegration_TransitionStatus drives a task through a legal
// multi-step sequence and confirms an illegal one is rejected with 409,
// end to end through the real stack.
func TestIntegration_TransitionStatus(t *testing.T) {
	srv, token := newIntegrationServer(t)

	created := decodeTask(t, doRequest(t, srv, token, http.MethodPost, "/tasks", `{"title":"Track me"}`))

	inProgressResp := doRequest(t, srv, token, http.MethodPatch, "/tasks/"+created.ID+"/status", `{"status":"in_progress"}`)
	assertStatus(t, inProgressResp, http.StatusOK)
	if got := decodeTask(t, inProgressResp).Status; got != StatusInProgress {
		t.Fatalf("status after transition = %q, want %q", got, StatusInProgress)
	}

	cancelledResp := doRequest(t, srv, token, http.MethodPatch, "/tasks/"+created.ID+"/status", `{"status":"cancelled"}`)
	assertStatus(t, cancelledResp, http.StatusOK)

	// cancelled -> done is not a legal transition (see service.go's
	// legalTransitions table).
	illegalResp := doRequest(t, srv, token, http.MethodPatch, "/tasks/"+created.ID+"/status", `{"status":"done"}`)
	assertStatus(t, illegalResp, http.StatusConflict)

	// The illegal attempt must not have changed the persisted status.
	persisted := decodeTask(t, doRequest(t, srv, token, http.MethodGet, "/tasks/"+created.ID, ""))
	if persisted.Status != StatusCancelled {
		t.Errorf("rejected transition changed persisted status: got %q, want %q", persisted.Status, StatusCancelled)
	}
}

// --- DELETE /tasks/{id} ---

// TestIntegration_DeleteTask verifies deletion is durable: gone from a
// later GET by ID, and gone from a later GET /tasks listing too.
func TestIntegration_DeleteTask(t *testing.T) {
	srv, token := newIntegrationServer(t)

	created := decodeTask(t, doRequest(t, srv, token, http.MethodPost, "/tasks", `{"title":"To delete"}`))

	deleteResp := doRequest(t, srv, token, http.MethodDelete, "/tasks/"+created.ID, "")
	assertStatus(t, deleteResp, http.StatusNoContent)

	remainingBody, err := io.ReadAll(deleteResp.Body)
	if err != nil {
		t.Fatalf("read DELETE body: %v", err)
	}
	if len(remainingBody) != 0 {
		t.Errorf("DELETE response body = %q, want empty", remainingBody)
	}

	// Persistence: a fresh, separate GET must now report it as gone.
	getResp := doRequest(t, srv, token, http.MethodGet, "/tasks/"+created.ID, "")
	assertStatus(t, getResp, http.StatusNotFound)

	// Persistence: it must also be gone from the listing.
	listResp := doRequest(t, srv, token, http.MethodGet, "/tasks", "")
	for _, task := range decodeTasks(t, listResp) {
		if task.ID == created.ID {
			t.Errorf("deleted task %q is still present in GET /tasks", created.ID)
		}
	}
}

func TestIntegration_DeleteTask_NotFound(t *testing.T) {
	srv, token := newIntegrationServer(t)

	resp := doRequest(t, srv, token, http.MethodDelete, "/tasks/does-not-exist", "")

	assertStatus(t, resp, http.StatusNotFound)
}
