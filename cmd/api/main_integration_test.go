package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JonasBorgesLM/task-api/internal/config"
	"github.com/JonasBorgesLM/task-api/internal/middleware"
	"github.com/JonasBorgesLM/task-api/internal/task"
)

// testConfig returns the config.Config every test in this file builds a
// server from: no DatabaseURL (so it always resolves to the in-memory
// Repositorys), but a real AuthSessionTTL — unlike every other field, a
// bare zero-value config.Config{} would leave this at 0, which makes
// user.Service.CreateSession issue tokens that are already expired by the
// time a subsequent request validates them (see openDatabase/config.Load,
// the only production path that actually defaults it).
func testConfig() config.Config {
	return config.Config{AuthSessionTTL: time.Hour}
}

// newTestServer builds the same *http.Server newServer builds in
// production, failing the test immediately if wiring fails (it shouldn't,
// for the config testConfig() every caller here passes) and registering
// the returned close function via t.Cleanup so tests never need to
// remember it themselves.
func newTestServer(t *testing.T, cfg config.Config, logger *slog.Logger) *http.Server {
	t.Helper()

	// Canceled at test cleanup so newServer's background cleanup goroutine
	// (runPeriodicCleanup) stops instead of leaking for the rest of the
	// test binary's run.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv, closeDB, err := newServer(ctx, cfg, logger)
	if err != nil {
		t.Fatalf("newServer() unexpected error: %v", err)
	}
	t.Cleanup(func() {
		if err := closeDB(); err != nil {
			t.Errorf("closeDB() unexpected error: %v", err)
		}
	})

	return srv
}

// registerAndLogin drives the real POST /auth/register and POST
// /auth/login endpoints newServer wires up, and returns a bearer token
// for the newly created user. Every task route requires one — see
// task.Handler.RegisterRoutes.
func registerAndLogin(t *testing.T, srv *httptest.Server) string {
	t.Helper()

	const body = `{"email":"integration@example.com","password":"password12345"}`

	regResp, err := srv.Client().Post(srv.URL+"/auth/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /auth/register: %v", err)
	}
	defer regResp.Body.Close()
	if regResp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(regResp.Body)
		t.Fatalf("POST /auth/register status = %d, body = %s", regResp.StatusCode, respBody)
	}

	loginResp, err := srv.Client().Post(srv.URL+"/auth/login", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /auth/login: %v", err)
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("POST /auth/login status = %d, body = %s", loginResp.StatusCode, respBody)
	}

	var loginBody struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&loginBody); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return loginBody.Token
}

// authedRequest builds an http.Request carrying the given bearer token.
func authedRequest(t *testing.T, token, method, url, body string) *http.Request {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("build %s request: %v", method, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// TestIntegration_TaskLifecycle drives newServer's wiring — the same
// composition run() uses, including the real auth flow — through a full
// register/login/create/read/update/complete/delete cycle over a real
// HTTP server. This is the only test that exercises the actual dependency
// wiring end to end; every other test in this repository talks to a fake
// Repository or Service.
func TestIntegration_TaskLifecycle(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, testConfig(), discardLogger()).Handler)
	defer srv.Close()

	client := srv.Client()
	token := registerAndLogin(t, srv)

	// --- Create ---
	createResp, err := client.Do(authedRequest(t, token, http.MethodPost, srv.URL+"/tasks",
		`{"title":"Integration task","description":"created via real stack"}`))
	if err != nil {
		t.Fatalf("POST /tasks: %v", err)
	}
	defer createResp.Body.Close()

	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /tasks status = %d, want %d", createResp.StatusCode, http.StatusCreated)
	}

	var created task.Task
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" {
		t.Fatal("POST /tasks: created task has empty ID")
	}
	if created.Status != task.StatusPending {
		t.Errorf("POST /tasks: Status = %q, want %q", created.Status, task.StatusPending)
	}

	// --- Read ---
	getResp, err := client.Do(authedRequest(t, token, http.MethodGet, srv.URL+"/tasks/"+created.ID, ""))
	if err != nil {
		t.Fatalf("GET /tasks/{id}: %v", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /tasks/{id} status = %d, want %d", getResp.StatusCode, http.StatusOK)
	}

	// --- List ---
	listResp, err := client.Do(authedRequest(t, token, http.MethodGet, srv.URL+"/tasks", ""))
	if err != nil {
		t.Fatalf("GET /tasks: %v", err)
	}
	defer listResp.Body.Close()

	var listed []task.Task
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("GET /tasks: returned %d tasks, want 1", len(listed))
	}

	// --- Update ---
	updateResp, err := client.Do(authedRequest(t, token, http.MethodPut, srv.URL+"/tasks/"+created.ID,
		`{"title":"Updated integration task","description":"updated via real stack"}`))
	if err != nil {
		t.Fatalf("PUT /tasks/{id}: %v", err)
	}
	defer updateResp.Body.Close()

	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /tasks/{id} status = %d, want %d", updateResp.StatusCode, http.StatusOK)
	}

	var updated task.Task
	if err := json.NewDecoder(updateResp.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.Title != "Updated integration task" {
		t.Errorf("PUT /tasks/{id}: Title = %q, want %q", updated.Title, "Updated integration task")
	}

	// --- Complete ---
	completeResp, err := client.Do(authedRequest(t, token, http.MethodPatch, srv.URL+"/tasks/"+created.ID+"/done", ""))
	if err != nil {
		t.Fatalf("PATCH /tasks/{id}/done: %v", err)
	}
	defer completeResp.Body.Close()

	if completeResp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH /tasks/{id}/done status = %d, want %d", completeResp.StatusCode, http.StatusOK)
	}

	var completed task.Task
	if err := json.NewDecoder(completeResp.Body).Decode(&completed); err != nil {
		t.Fatalf("decode complete response: %v", err)
	}
	if completed.Status != task.StatusDone {
		t.Errorf("PATCH /tasks/{id}/done: Status = %q, want %q", completed.Status, task.StatusDone)
	}

	// --- Delete ---
	deleteResp, err := client.Do(authedRequest(t, token, http.MethodDelete, srv.URL+"/tasks/"+created.ID, ""))
	if err != nil {
		t.Fatalf("DELETE /tasks/{id}: %v", err)
	}
	defer deleteResp.Body.Close()

	if deleteResp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /tasks/{id} status = %d, want %d", deleteResp.StatusCode, http.StatusNoContent)
	}

	// --- Verify gone ---
	goneResp, err := client.Do(authedRequest(t, token, http.MethodGet, srv.URL+"/tasks/"+created.ID, ""))
	if err != nil {
		t.Fatalf("GET /tasks/{id} after delete: %v", err)
	}
	defer goneResp.Body.Close()

	if goneResp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /tasks/{id} after delete: status = %d, want %d", goneResp.StatusCode, http.StatusNotFound)
	}
}

// TestIntegration_CreateTask_RequiresAuth verifies that a task route
// reached with no Authorization header is rejected before it ever reaches
// task.Handler — the real user.RequireAuth middleware, wired exactly as
// newServer wires it in production.
func TestIntegration_CreateTask_RequiresAuth(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, testConfig(), discardLogger()).Handler)
	defer srv.Close()

	resp, err := srv.Client().Post(srv.URL+"/tasks", "application/json", strings.NewReader(`{"title":"T"}`))
	if err != nil {
		t.Fatalf("POST /tasks: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST /tasks with no Authorization header: status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestIntegration_CreateTask_EmptyTitle_Returns400 verifies that the real
// Service's validation rule is reachable through the real Handler, over a
// real HTTP request.
func TestIntegration_CreateTask_EmptyTitle_Returns400(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, testConfig(), discardLogger()).Handler)
	defer srv.Close()
	token := registerAndLogin(t, srv)

	resp, err := srv.Client().Do(authedRequest(t, token, http.MethodPost, srv.URL+"/tasks", `{"title":""}`))
	if err != nil {
		t.Fatalf("POST /tasks: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("POST /tasks with empty title: status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

// TestIntegration_MiddlewareWired verifies that the same middleware chain
// built by newServer (and used by run()) is actually applied: every
// response must carry an X-Request-ID header. This guards against someone
// changing newServer and forgetting to keep the middleware chain wired to
// the server.
func TestIntegration_MiddlewareWired(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, testConfig(), discardLogger()).Handler)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get(middleware.HeaderRequestID); got == "" {
		t.Error("response is missing the X-Request-ID header — middleware chain not applied")
	}
}

// TestIntegration_ReadinessEndpoint verifies that newServer registers GET
// /health/ready and that, for the in-memory-store configuration these
// tests build (testConfig() has no DatabaseURL), it reports ready —
// there's no external dependency for it to be unready about.
func TestIntegration_ReadinessEndpoint(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, testConfig(), discardLogger()).Handler)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/health/ready")
	if err != nil {
		t.Fatalf("GET /health/ready: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /health/ready status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// TestIntegration_DebugVars verifies that newServer registers the stdlib
// expvar handler at GET /debug/vars, giving baseline runtime observability
// (goroutine count, memstats, GC stats) without pulling in an external
// metrics dependency — and that, unlike /health and /health/ready, it is
// only reachable with a valid session token.
func TestIntegration_DebugVars(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, testConfig(), discardLogger()).Handler)
	defer srv.Close()

	token := registerAndLogin(t, srv)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/debug/vars", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /debug/vars: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /debug/vars status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /debug/vars body: %v", err)
	}
	for _, key := range []string{"cmdline", "memstats"} {
		if _, ok := body[key]; !ok {
			t.Errorf("/debug/vars response is missing expected key %q", key)
		}
	}
}

// TestIntegration_DebugVars_RequiresAuth guards the other half of the
// contract: expvar leaks the process command line and full runtime
// statistics, so an unauthenticated caller must not reach it. The health
// routes are deliberately public (an orchestrator probe has no token to
// present) — this asserts /debug/vars is not.
func TestIntegration_DebugVars_RequiresAuth(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, testConfig(), discardLogger()).Handler)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/debug/vars")
	if err != nil {
		t.Fatalf("GET /debug/vars: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated GET /debug/vars status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	// The runtime detail expvar would have served must not appear in the
	// rejection body.
	rejected, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	for _, leaked := range []string{"cmdline", "memstats"} {
		if strings.Contains(string(rejected), leaked) {
			t.Errorf("unauthenticated rejection leaked %q: %s", leaked, rejected)
		}
	}
}
