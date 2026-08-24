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
	return config.Config{
		AuthSessionTTL: time.Hour,

		// Rate limits belong to the same category as AuthSessionTTL: a
		// zero value is not "unset, use a default" but an actively
		// hostile setting. ratelimit.New clamps a burst below 1 up to 1
		// and treats a non-positive refill rate as "never refills", so a
		// bare config.Config{} yields a server that serves exactly one
		// request per limiter and then answers 429 forever. That is the
		// library failing closed on misconfiguration, which is the right
		// behavior in production and a trap in a test helper.
		//
		// The values here are far above anything a test in this package
		// generates, so a 429 in one of them is a real regression rather
		// than the helper's own doing. The tests that exercise limiting
		// on purpose set their own tight values.
		RateLimitBurst:      1000,
		RateLimitPerSec:     1000,
		AuthRateLimitBurst:  1000,
		AuthRateLimitPerSec: 1000,
		UserRateLimitBurst:  1000,
		UserRateLimitPerSec: 1000,
	}
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
	return registerAndLoginAs(t, srv, "integration@example.com")
}

// registerAndLoginAs is registerAndLogin for a caller that needs more
// than one distinct account — per-user behavior cannot be observed with a
// single identity.
func registerAndLoginAs(t *testing.T, srv *httptest.Server, email string) string {
	t.Helper()

	body := `{"email":"` + email + `","password":"password12345"}`

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

// --- CORS ---
//
// These specifically exercise the real newServer wiring — not just
// middleware.CORS in isolation (see internal/middleware/cors_test.go) —
// because the failure mode CORS exists to fix (a browser-based Swagger UI
// on one origin, http://localhost:8082, unable to call the API on
// another, http://localhost:8080) only shows up once CORS is positioned
// correctly relative to routing: a preflight OPTIONS request for a route
// that only registers POST/GET/etc. must be answered before mux dispatch,
// not 404 from it.

func TestIntegration_CORS_Disabled_NoHeaders(t *testing.T) {
	// testConfig() leaves CORSAllowedOrigins unset — the default every
	// deployment has until CORS_ALLOWED_ORIGINS is explicitly configured.
	srv := httptest.NewServer(newTestServer(t, testConfig(), discardLogger()).Handler)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/health", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Origin", "http://localhost:8082")

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("CORS disabled but Access-Control-Allow-Origin = %q, want none", got)
	}
}

func TestIntegration_CORS_Enabled_AllowedOrigin_ActualRequest(t *testing.T) {
	cfg := testConfig()
	cfg.CORSAllowedOrigins = []string{"http://localhost:8082"}
	srv := httptest.NewServer(newTestServer(t, cfg, discardLogger()).Handler)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/health", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Origin", "http://localhost:8082")

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:8082" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "http://localhost:8082")
	}
}

// TestIntegration_CORS_Enabled_PreflightForRegisteredRoute is the exact
// scenario this feature was built for: a browser about to POST /tasks
// (which registers no OPTIONS handler of its own — see
// task.Handler.RegisterRoutes) sends a preflight OPTIONS request first.
// Before CORS existed, that request 404'd from mux with no
// Access-Control-* headers, so the browser blocked the real POST from
// ever being sent.
func TestIntegration_CORS_Enabled_PreflightForRegisteredRoute(t *testing.T) {
	cfg := testConfig()
	cfg.CORSAllowedOrigins = []string{"http://localhost:8082"}
	srv := httptest.NewServer(newTestServer(t, cfg, discardLogger()).Handler)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodOptions, srv.URL+"/tasks", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Origin", "http://localhost:8082")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("OPTIONS /tasks: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("preflight status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:8082" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "http://localhost:8082")
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); !strings.Contains(got, "POST") {
		t.Errorf("Access-Control-Allow-Methods = %q, want it to include POST", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(strings.ToLower(got), "authorization") {
		t.Errorf("Access-Control-Allow-Headers = %q, want it to include Authorization", got)
	}
}

// TestIntegration_CORS_Enabled_DisallowedOrigin_PreflightStill404s proves
// enabling CORS for one origin doesn't change behavior for every other
// origin: an unlisted origin's preflight still falls through to mux
// exactly as it always did (no route registers OPTIONS, so 404), with no
// Access-Control-Allow-Origin header handed out.
func TestIntegration_CORS_Enabled_DisallowedOrigin_PreflightStill404s(t *testing.T) {
	cfg := testConfig()
	cfg.CORSAllowedOrigins = []string{"http://localhost:8082"}
	srv := httptest.NewServer(newTestServer(t, cfg, discardLogger()).Handler)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodOptions, srv.URL+"/tasks", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Origin", "http://evil.example")

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("OPTIONS /tasks: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("disallowed origin got Access-Control-Allow-Origin = %q, want none", got)
	}
}

// --- rate limiting ---
//
// These drive the composed limiters through a real HTTP server rather
// than the ratelimit package in isolation: what is worth pinning here is
// not that a token bucket counts correctly (the library tests that) but
// that the three tiers are mounted where newServer claims they are, in an
// order that actually produces the intended key.
//
// Every config below sets a refill rate low enough to be irrelevant for
// the handful of requests each test makes, so a bucket that starts empty
// stays empty. A rate fast enough to refill mid-test would make these
// pass or fail on timing.

// rateLimitedConfig returns testConfig() with one tier tightened to the
// given burst and effectively no refill.
func rateLimitedConfig(tier string, burst int) config.Config {
	cfg := testConfig()
	switch tier {
	case "global":
		cfg.RateLimitBurst, cfg.RateLimitPerSec = burst, 0.001
	case "auth":
		cfg.AuthRateLimitBurst, cfg.AuthRateLimitPerSec = burst, 0.001
	case "user":
		cfg.UserRateLimitBurst, cfg.UserRateLimitPerSec = burst, 0.001
	default:
		panic("unknown tier " + tier)
	}
	return cfg
}

// TestIntegration_RateLimit_ProtectsTaskRoutes is the regression this
// whole tier exists for: before the limiters were composed globally, only
// /auth/register and /auth/login were bounded and every task route could
// be called without limit.
func TestIntegration_RateLimit_ProtectsTaskRoutes(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, rateLimitedConfig("user", 2), discardLogger()).Handler)
	defer srv.Close()

	token := registerAndLogin(t, srv)

	var statuses []int
	for range 3 {
		resp, err := srv.Client().Do(authedRequest(t, token, http.MethodGet, srv.URL+"/tasks", ""))
		if err != nil {
			t.Fatalf("GET /tasks: %v", err)
		}
		resp.Body.Close()
		statuses = append(statuses, resp.StatusCode)
	}

	if statuses[0] != http.StatusOK || statuses[1] != http.StatusOK {
		t.Errorf("first two GET /tasks = %v, want both 200 (burst is 2)", statuses[:2])
	}
	if statuses[2] != http.StatusTooManyRequests {
		t.Errorf("third GET /tasks = %d, want 429", statuses[2])
	}
}

// TestIntegration_RateLimit_PerUserBucketsAreIndependent pins the key
// function, not the counting: both users here share one client address,
// so a limiter still keyed by address would reject the second user's
// request and pass this test's premise by accident.
func TestIntegration_RateLimit_PerUserBucketsAreIndependent(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, rateLimitedConfig("user", 1), discardLogger()).Handler)
	defer srv.Close()

	tokenA := registerAndLoginAs(t, srv, "user-a@example.com")
	tokenB := registerAndLoginAs(t, srv, "user-b@example.com")

	// Drain user A's bucket: one allowed, one rejected.
	for i, want := range []int{http.StatusOK, http.StatusTooManyRequests} {
		resp, err := srv.Client().Do(authedRequest(t, tokenA, http.MethodGet, srv.URL+"/tasks", ""))
		if err != nil {
			t.Fatalf("user A GET /tasks #%d: %v", i+1, err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("user A GET /tasks #%d = %d, want %d", i+1, resp.StatusCode, want)
		}
	}

	// User B is untouched by user A having exhausted theirs.
	resp, err := srv.Client().Do(authedRequest(t, tokenB, http.MethodGet, srv.URL+"/tasks", ""))
	if err != nil {
		t.Fatalf("user B GET /tasks: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("user B GET /tasks = %d, want 200 — buckets are keyed by user, not by address", resp.StatusCode)
	}
}

// TestIntegration_RateLimit_GlobalAppliesBeforeAuthentication covers why
// the global tier cannot simply be replaced by the per-user one: an
// unauthenticated caller has no user ID to key on, and the session lookup
// RequireAuth performs is itself work worth bounding. A request rejected
// here never reaches that lookup, so the response is 429 rather than 401.
func TestIntegration_RateLimit_GlobalAppliesBeforeAuthentication(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, rateLimitedConfig("global", 1), discardLogger()).Handler)
	defer srv.Close()

	first, err := srv.Client().Get(srv.URL + "/tasks")
	if err != nil {
		t.Fatalf("first GET /tasks: %v", err)
	}
	first.Body.Close()
	if first.StatusCode != http.StatusUnauthorized {
		t.Fatalf("first GET /tasks = %d, want 401 (within burst, so auth decides)", first.StatusCode)
	}

	second, err := srv.Client().Get(srv.URL + "/tasks")
	if err != nil {
		t.Fatalf("second GET /tasks: %v", err)
	}
	second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests {
		t.Errorf("second GET /tasks = %d, want 429 — the global tier must reject before authentication runs", second.StatusCode)
	}
}

// TestIntegration_RateLimit_AuthRoutesHaveTheirOwnTier checks that
// /auth/login is bounded by the auth tier even when the global tier is
// wide open — the two exist to answer different threats.
func TestIntegration_RateLimit_AuthRoutesHaveTheirOwnTier(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, rateLimitedConfig("auth", 1), discardLogger()).Handler)
	defer srv.Close()

	const body = `{"email":"nobody@example.com","password":"wrong-password-here"}`

	first, err := srv.Client().Post(srv.URL+"/auth/login", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("first POST /auth/login: %v", err)
	}
	first.Body.Close()
	if first.StatusCode != http.StatusUnauthorized {
		t.Fatalf("first POST /auth/login = %d, want 401", first.StatusCode)
	}

	second, err := srv.Client().Post(srv.URL+"/auth/login", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("second POST /auth/login: %v", err)
	}
	second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests {
		t.Errorf("second POST /auth/login = %d, want 429", second.StatusCode)
	}
}

// TestIntegration_RateLimit_RejectionCarriesSecurityHeaders guards the
// chain order: secureheaders is mounted outside the limiter precisely so
// that a response the limiter writes itself — never reaching a handler —
// still carries the headers.
func TestIntegration_RateLimit_RejectionCarriesSecurityHeaders(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, rateLimitedConfig("global", 1), discardLogger()).Handler)
	defer srv.Close()

	// Not /health: that route deliberately bypasses the global limiter
	// (see newServer), so it can never produce the 429 this test needs.
	for range 2 {
		resp, err := srv.Client().Get(srv.URL + "/tasks")
		if err != nil {
			t.Fatalf("GET /tasks: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			for _, name := range []string{"Content-Security-Policy", "X-Frame-Options", "X-Content-Type-Options"} {
				if resp.Header.Get(name) == "" {
					t.Errorf("429 response is missing %s", name)
				}
			}
			return
		}
	}
	t.Fatal("never observed a 429 — the global limiter did not engage")
}

// TestIntegration_RateLimit_HealthProbesAreExempt pins the exemption that
// keeps a traffic burst from being escalated into a restart loop: an
// orchestrator reads 429 as "unhealthy" and kills the replica, so the
// probes must answer 200 even once every other route is rejecting.
func TestIntegration_RateLimit_HealthProbesAreExempt(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, rateLimitedConfig("global", 1), discardLogger()).Handler)
	defer srv.Close()

	// Drain the global bucket on a route that is subject to it.
	for range 2 {
		resp, err := srv.Client().Get(srv.URL + "/tasks")
		if err != nil {
			t.Fatalf("GET /tasks: %v", err)
		}
		resp.Body.Close()
	}

	// Confirm the bucket really is empty, so a 200 below means the
	// exemption rather than a limiter that never engaged.
	drained, err := srv.Client().Get(srv.URL + "/tasks")
	if err != nil {
		t.Fatalf("GET /tasks: %v", err)
	}
	drained.Body.Close()
	if drained.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("GET /tasks = %d, want 429 — the bucket was expected to be empty by now", drained.StatusCode)
	}

	for _, path := range []string{"/health", "/health/ready"} {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 even with the global bucket empty", path, resp.StatusCode)
		}
	}
}

// --- trusted proxies ---

// forgedRequest builds a request to path carrying an X-Forwarded-For the
// client chose for itself. This is the shape of the attack the trusted
// proxy list exists to constrain: a limiter that believes this header
// unconditionally gives every request a fresh bucket.
func forgedRequest(t *testing.T, url, forwardedFor string) *http.Request {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("X-Forwarded-For", forwardedFor)
	return req
}

// TestIntegration_TrustedProxies_ForgedHeaderIgnoredWhenPeerIsNotTrusted
// is the hostile case: with no trusted proxies configured, a client that
// invents a new X-Forwarded-For per request must still land in the same
// bucket. A regression here does not look like a failure — it looks like
// a rate limiter that simply never triggers.
func TestIntegration_TrustedProxies_ForgedHeaderIgnoredWhenPeerIsNotTrusted(t *testing.T) {
	cfg := rateLimitedConfig("global", 1)
	// Explicit: this is the default, and it is what the test is about.
	cfg.TrustedProxies = nil

	srv := httptest.NewServer(newTestServer(t, cfg, discardLogger()).Handler)
	defer srv.Close()

	first, err := srv.Client().Do(forgedRequest(t, srv.URL+"/tasks", "203.0.113.1"))
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	first.Body.Close()
	if first.StatusCode != http.StatusUnauthorized {
		t.Fatalf("first request = %d, want 401 (within burst)", first.StatusCode)
	}

	// A different forged address. The peer is unchanged, so the bucket
	// must be too.
	second, err := srv.Client().Do(forgedRequest(t, srv.URL+"/tasks", "203.0.113.2"))
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests {
		t.Errorf("second request with a different forged X-Forwarded-For = %d, want 429 — the header must be ignored from an untrusted peer",
			second.StatusCode)
	}
}

// TestIntegration_TrustedProxies_HeaderHonouredWhenPeerIsTrusted is the
// other half: once the peer really is a declared proxy, two clients
// behind it must get separate buckets instead of collapsing into the
// proxy's single one. httptest connects over loopback, so trusting
// loopback is what makes this test's premise true.
func TestIntegration_TrustedProxies_HeaderHonouredWhenPeerIsTrusted(t *testing.T) {
	cfg := rateLimitedConfig("global", 1)
	cfg.TrustedProxies = []string{"127.0.0.1/32", "::1/128"}

	srv := httptest.NewServer(newTestServer(t, cfg, discardLogger()).Handler)
	defer srv.Close()

	// Drain the bucket belonging to the first client behind the proxy.
	for i, want := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
		resp, err := srv.Client().Do(forgedRequest(t, srv.URL+"/tasks", "203.0.113.1"))
		if err != nil {
			t.Fatalf("client 1 request #%d: %v", i+1, err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("client 1 request #%d = %d, want %d", i+1, resp.StatusCode, want)
		}
	}

	// A different client behind the same proxy is unaffected.
	resp, err := srv.Client().Do(forgedRequest(t, srv.URL+"/tasks", "203.0.113.2"))
	if err != nil {
		t.Fatalf("client 2 request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("client 2 request = %d, want 401 — clients behind a trusted proxy must not share one bucket", resp.StatusCode)
	}
}

// TestNewServer_RejectsDangerousTrustedProxies pins that a list which
// would disable the limiter fails startup instead of being accepted. The
// default route is the specific mistake: trusting 0.0.0.0/0 means every
// client is a proxy, so every client picks its own key.
func TestNewServer_RejectsDangerousTrustedProxies(t *testing.T) {
	cfg := testConfig()
	cfg.TrustedProxies = []string{"0.0.0.0/0"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, closeAll, err := newServer(ctx, cfg, discardLogger())
	if err == nil {
		if closeAll != nil {
			closeAll()
		}
		_ = srv
		t.Fatal("newServer() error = nil, want a refusal to trust the default route")
	}
}
