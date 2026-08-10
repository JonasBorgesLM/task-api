package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JonasBorgesLM/task-api/config"
	"github.com/JonasBorgesLM/task-api/middleware"
	"github.com/JonasBorgesLM/task-api/task"
)

// TestIntegration_TaskLifecycle drives newServer's wiring — the same
// composition run() uses — through a full create/read/update/complete/
// delete cycle over a real HTTP server. This is the only test that
// exercises the actual dependency wiring end to end; every other test in
// this repository talks to a fake Repository or Service.
func TestIntegration_TaskLifecycle(t *testing.T) {
	srv := httptest.NewServer(newServer(config.Config{}, discardLogger()).Handler)
	defer srv.Close()

	client := srv.Client()

	// --- Create ---
	createResp, err := client.Post(srv.URL+"/tasks", "application/json",
		strings.NewReader(`{"title":"Integration task","description":"created via real stack"}`))
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
	getResp, err := client.Get(srv.URL + "/tasks/" + created.ID)
	if err != nil {
		t.Fatalf("GET /tasks/{id}: %v", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /tasks/{id} status = %d, want %d", getResp.StatusCode, http.StatusOK)
	}

	// --- List ---
	listResp, err := client.Get(srv.URL + "/tasks")
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
	updateReq, err := http.NewRequest(http.MethodPut, srv.URL+"/tasks/"+created.ID,
		strings.NewReader(`{"title":"Updated integration task","description":"updated via real stack"}`))
	if err != nil {
		t.Fatalf("build PUT request: %v", err)
	}
	updateReq.Header.Set("Content-Type", "application/json")

	updateResp, err := client.Do(updateReq)
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
	completeReq, err := http.NewRequest(http.MethodPatch, srv.URL+"/tasks/"+created.ID+"/done", nil)
	if err != nil {
		t.Fatalf("build PATCH request: %v", err)
	}

	completeResp, err := client.Do(completeReq)
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
	deleteReq, err := http.NewRequest(http.MethodDelete, srv.URL+"/tasks/"+created.ID, nil)
	if err != nil {
		t.Fatalf("build DELETE request: %v", err)
	}

	deleteResp, err := client.Do(deleteReq)
	if err != nil {
		t.Fatalf("DELETE /tasks/{id}: %v", err)
	}
	defer deleteResp.Body.Close()

	if deleteResp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /tasks/{id} status = %d, want %d", deleteResp.StatusCode, http.StatusNoContent)
	}

	// --- Verify gone ---
	goneResp, err := client.Get(srv.URL + "/tasks/" + created.ID)
	if err != nil {
		t.Fatalf("GET /tasks/{id} after delete: %v", err)
	}
	defer goneResp.Body.Close()

	if goneResp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /tasks/{id} after delete: status = %d, want %d", goneResp.StatusCode, http.StatusNotFound)
	}
}

// TestIntegration_CreateTask_EmptyTitle_Returns400 verifies that the real
// Service's validation rule is reachable through the real Handler, over a
// real HTTP request.
func TestIntegration_CreateTask_EmptyTitle_Returns400(t *testing.T) {
	srv := httptest.NewServer(newServer(config.Config{}, discardLogger()).Handler)
	defer srv.Close()

	resp, err := srv.Client().Post(srv.URL+"/tasks", "application/json", strings.NewReader(`{"title":""}`))
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
	srv := httptest.NewServer(newServer(config.Config{}, discardLogger()).Handler)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/tasks")
	if err != nil {
		t.Fatalf("GET /tasks: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get(middleware.HeaderRequestID); got == "" {
		t.Error("response is missing the X-Request-ID header — middleware chain not applied")
	}
}

// TestIntegration_DebugVars verifies that newServer registers the stdlib
// expvar handler at GET /debug/vars, giving at least baseline runtime
// observability (goroutine count, memstats, GC stats) without pulling in
// an external metrics dependency.
func TestIntegration_DebugVars(t *testing.T) {
	srv := httptest.NewServer(newServer(config.Config{}, discardLogger()).Handler)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/debug/vars")
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
