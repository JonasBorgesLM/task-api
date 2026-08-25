package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JonasBorgesLM/task-api/internal/config"
	"github.com/JonasBorgesLM/task-api/internal/middleware"
	"github.com/JonasBorgesLM/task-api/internal/task"
)

// apiPrefix is where the versioned contract is mounted. The health
// probes and /debug/vars are deliberately *not* under it — they are
// operational endpoints rather than part of the contract a client codes
// against — so tests hitting those keep using bare paths, and that
// asymmetry is the point rather than an oversight.
const apiPrefix = "/v1"

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

	regResp, err := srv.Client().Post(srv.URL+apiPrefix+"/auth/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /auth/register: %v", err)
	}
	defer regResp.Body.Close()
	if regResp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(regResp.Body)
		t.Fatalf("POST /auth/register status = %d, body = %s", regResp.StatusCode, respBody)
	}

	loginResp, err := srv.Client().Post(srv.URL+apiPrefix+"/auth/login", "application/json", strings.NewReader(body))
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
	createResp, err := client.Do(authedRequest(t, token, http.MethodPost, srv.URL+apiPrefix+"/tasks",
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
	getResp, err := client.Do(authedRequest(t, token, http.MethodGet, srv.URL+apiPrefix+"/tasks/"+created.ID, ""))
	if err != nil {
		t.Fatalf("GET /tasks/{id}: %v", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /tasks/{id} status = %d, want %d", getResp.StatusCode, http.StatusOK)
	}

	// --- List ---
	listResp, err := client.Do(authedRequest(t, token, http.MethodGet, srv.URL+apiPrefix+"/tasks", ""))
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
	updateResp, err := client.Do(authedRequest(t, token, http.MethodPut, srv.URL+apiPrefix+"/tasks/"+created.ID,
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
	completeResp, err := client.Do(authedRequest(t, token, http.MethodPatch, srv.URL+apiPrefix+"/tasks/"+created.ID+"/done", ""))
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
	deleteResp, err := client.Do(authedRequest(t, token, http.MethodDelete, srv.URL+apiPrefix+"/tasks/"+created.ID, ""))
	if err != nil {
		t.Fatalf("DELETE /tasks/{id}: %v", err)
	}
	defer deleteResp.Body.Close()

	if deleteResp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /tasks/{id} status = %d, want %d", deleteResp.StatusCode, http.StatusNoContent)
	}

	// --- Verify gone ---
	goneResp, err := client.Do(authedRequest(t, token, http.MethodGet, srv.URL+apiPrefix+"/tasks/"+created.ID, ""))
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

	resp, err := srv.Client().Post(srv.URL+apiPrefix+"/tasks", "application/json", strings.NewReader(`{"title":"T"}`))
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

	resp, err := srv.Client().Do(authedRequest(t, token, http.MethodPost, srv.URL+apiPrefix+"/tasks", `{"title":""}`))
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

	req, err := http.NewRequest(http.MethodOptions, srv.URL+apiPrefix+"/tasks", nil)
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

	req, err := http.NewRequest(http.MethodOptions, srv.URL+apiPrefix+"/tasks", nil)
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
		resp, err := srv.Client().Do(authedRequest(t, token, http.MethodGet, srv.URL+apiPrefix+"/tasks", ""))
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
		resp, err := srv.Client().Do(authedRequest(t, tokenA, http.MethodGet, srv.URL+apiPrefix+"/tasks", ""))
		if err != nil {
			t.Fatalf("user A GET /tasks #%d: %v", i+1, err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("user A GET /tasks #%d = %d, want %d", i+1, resp.StatusCode, want)
		}
	}

	// User B is untouched by user A having exhausted theirs.
	resp, err := srv.Client().Do(authedRequest(t, tokenB, http.MethodGet, srv.URL+apiPrefix+"/tasks", ""))
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

	first, err := srv.Client().Get(srv.URL + apiPrefix + "/tasks")
	if err != nil {
		t.Fatalf("first GET /tasks: %v", err)
	}
	first.Body.Close()
	if first.StatusCode != http.StatusUnauthorized {
		t.Fatalf("first GET /tasks = %d, want 401 (within burst, so auth decides)", first.StatusCode)
	}

	second, err := srv.Client().Get(srv.URL + apiPrefix + "/tasks")
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

	first, err := srv.Client().Post(srv.URL+apiPrefix+"/auth/login", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("first POST /auth/login: %v", err)
	}
	first.Body.Close()
	if first.StatusCode != http.StatusUnauthorized {
		t.Fatalf("first POST /auth/login = %d, want 401", first.StatusCode)
	}

	second, err := srv.Client().Post(srv.URL+apiPrefix+"/auth/login", "application/json", strings.NewReader(body))
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
		resp, err := srv.Client().Get(srv.URL + apiPrefix + "/tasks")
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
		resp, err := srv.Client().Get(srv.URL + apiPrefix + "/tasks")
		if err != nil {
			t.Fatalf("GET /tasks: %v", err)
		}
		resp.Body.Close()
	}

	// Confirm the bucket really is empty, so a 200 below means the
	// exemption rather than a limiter that never engaged.
	drained, err := srv.Client().Get(srv.URL + apiPrefix + "/tasks")
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

	first, err := srv.Client().Do(forgedRequest(t, srv.URL+apiPrefix+"/tasks", "203.0.113.1"))
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	first.Body.Close()
	if first.StatusCode != http.StatusUnauthorized {
		t.Fatalf("first request = %d, want 401 (within burst)", first.StatusCode)
	}

	// A different forged address. The peer is unchanged, so the bucket
	// must be too.
	second, err := srv.Client().Do(forgedRequest(t, srv.URL+apiPrefix+"/tasks", "203.0.113.2"))
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
		resp, err := srv.Client().Do(forgedRequest(t, srv.URL+apiPrefix+"/tasks", "203.0.113.1"))
		if err != nil {
			t.Fatalf("client 1 request #%d: %v", i+1, err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("client 1 request #%d = %d, want %d", i+1, resp.StatusCode, want)
		}
	}

	// A different client behind the same proxy is unaffected.
	resp, err := srv.Client().Do(forgedRequest(t, srv.URL+apiPrefix+"/tasks", "203.0.113.2"))
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

// --- attachments ---

// attachmentConfig returns a testConfig with attachments enabled against
// a temporary storage root. The root is created here because
// NewFSBlobStore deliberately refuses a missing one.
func attachmentConfig(t *testing.T) config.Config {
	t.Helper()

	cfg := testConfig()
	cfg.AttachmentStorageDir = t.TempDir()
	cfg.AttachmentMaxBytes = 4096
	return cfg
}

// uploadFile posts one file to a task through the real multipart path.
func uploadFile(t *testing.T, srv *httptest.Server, token, taskID, filename string, content []byte) *http.Response {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+apiPrefix+"/tasks/"+taskID+"/attachments", &body)
	if err != nil {
		t.Fatalf("build upload request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	return resp
}

// createTask drives POST /tasks and returns the new task's ID.
func createTask(t *testing.T, srv *httptest.Server, token string) string {
	t.Helper()

	resp, err := srv.Client().Do(authedRequest(t, token, http.MethodPost, srv.URL+apiPrefix+"/tasks", `{"title":"with attachments"}`))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /tasks status = %d, body = %s", resp.StatusCode, respBody)
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created task: %v", err)
	}
	return created.ID
}

// TestIntegration_AttachmentLifecycle drives the whole feature through
// the real wiring: upload, list, download.
func TestIntegration_AttachmentLifecycle(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, attachmentConfig(t), discardLogger()).Handler)
	defer srv.Close()

	token := registerAndLogin(t, srv)
	taskID := createTask(t, srv, token)

	content := append([]byte("%PDF-1.7\n"), []byte("report body")...)

	uploadResp := uploadFile(t, srv, token, taskID, "report.pdf", content)
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(uploadResp.Body)
		t.Fatalf("upload status = %d, body = %s", uploadResp.StatusCode, respBody)
	}

	var uploaded struct {
		StorageKey       string `json:"storage_key"`
		OriginalFilename string `json:"original_filename"`
		ContentType      string `json:"content_type"`
		SizeBytes        int64  `json:"size_bytes"`
	}
	if err := json.NewDecoder(uploadResp.Body).Decode(&uploaded); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if uploaded.ContentType != "application/pdf" {
		t.Errorf("content_type = %q, want application/pdf", uploaded.ContentType)
	}
	if uploaded.SizeBytes != int64(len(content)) {
		t.Errorf("size_bytes = %d, want %d", uploaded.SizeBytes, len(content))
	}

	// List.
	listResp, err := srv.Client().Do(authedRequest(t, token, http.MethodGet, srv.URL+apiPrefix+"/tasks/"+taskID+"/attachments", ""))
	if err != nil {
		t.Fatalf("list attachments: %v", err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listResp.StatusCode)
	}
	var listed []struct {
		StorageKey string `json:"storage_key"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed) != 1 || listed[0].StorageKey != uploaded.StorageKey {
		t.Fatalf("list = %+v, want exactly the uploaded attachment", listed)
	}

	// Download.
	dlResp, err := srv.Client().Do(authedRequest(t, token, http.MethodGet, srv.URL+apiPrefix+"/files/"+uploaded.StorageKey, ""))
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d, want 200", dlResp.StatusCode)
	}

	got, _ := io.ReadAll(dlResp.Body)
	if !bytes.Equal(got, content) {
		t.Errorf("downloaded bytes = %q, want %q", got, content)
	}

	// Content-Disposition: attachment is what stops a browser rendering
	// user-uploaded bytes in this API's own origin.
	if got := dlResp.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Errorf("Content-Disposition = %q, want it to start with \"attachment\"", got)
	}
	if got := dlResp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff — a browser must not re-decide the type", got)
	}
}

// TestIntegration_Attachment_CrossUserIsRefused is the ownership rule
// end to end, on both routes that accept an identifier the other user
// legitimately holds.
func TestIntegration_Attachment_CrossUserIsRefused(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, attachmentConfig(t), discardLogger()).Handler)
	defer srv.Close()

	ownerToken := registerAndLoginAs(t, srv, "owner@example.com")
	strangerToken := registerAndLoginAs(t, srv, "stranger@example.com")

	taskID := createTask(t, srv, ownerToken)

	uploadResp := uploadFile(t, srv, ownerToken, taskID, "report.pdf", []byte("%PDF-1.7\nbody"))
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusCreated {
		t.Fatalf("owner upload status = %d, want 201", uploadResp.StatusCode)
	}
	var uploaded struct {
		StorageKey string `json:"storage_key"`
	}
	if err := json.NewDecoder(uploadResp.Body).Decode(&uploaded); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}

	// The stranger holds a real, valid storage key and must still be
	// refused — indistinguishably from a key that names nothing.
	dlResp, err := srv.Client().Do(authedRequest(t, strangerToken, http.MethodGet, srv.URL+apiPrefix+"/files/"+uploaded.StorageKey, ""))
	if err != nil {
		t.Fatalf("stranger download: %v", err)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != http.StatusNotFound {
		t.Errorf("stranger download status = %d, want 404", dlResp.StatusCode)
	}

	listResp, err := srv.Client().Do(authedRequest(t, strangerToken, http.MethodGet, srv.URL+apiPrefix+"/tasks/"+taskID+"/attachments", ""))
	if err != nil {
		t.Fatalf("stranger list: %v", err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusNotFound {
		t.Errorf("stranger list status = %d, want 404", listResp.StatusCode)
	}

	upResp := uploadFile(t, srv, strangerToken, taskID, "evil.pdf", []byte("%PDF-1.7\nx"))
	defer upResp.Body.Close()
	if upResp.StatusCode != http.StatusNotFound {
		t.Errorf("stranger upload status = %d, want 404", upResp.StatusCode)
	}
}

func TestIntegration_Attachment_RejectsDisallowedType(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, attachmentConfig(t), discardLogger()).Handler)
	defer srv.Close()

	token := registerAndLogin(t, srv)
	taskID := createTask(t, srv, token)

	// An ELF binary, labelled as a PNG. The label is a claim; the bytes
	// are what the allow-list is applied to.
	resp := uploadFile(t, srv, token, taskID, "innocent.png", []byte("\x7fELF\x02\x01\x01\x00binary"))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("upload of a mislabelled binary: status = %d, want 400", resp.StatusCode)
	}
}

// TestIntegration_Attachment_DisabledByDefault pins the opt-in: with no
// storage directory configured the routes do not exist at all, rather
// than existing and failing on every request.
func TestIntegration_Attachment_DisabledByDefault(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, testConfig(), discardLogger()).Handler)
	defer srv.Close()

	token := registerAndLogin(t, srv)
	taskID := createTask(t, srv, token)

	resp := uploadFile(t, srv, token, taskID, "report.pdf", []byte("%PDF-1.7\n"))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("upload with attachments disabled: status = %d, want 404", resp.StatusCode)
	}
}

// --- API versioning ---

// TestIntegration_Versioning_ContractIsUnderV1 pins which routes moved
// and which deliberately did not.
//
// The split is the decision, not an implementation detail: the versioned
// paths are the contract a client codes against, while the health probes
// and /debug/vars are operational. An orchestrator probe does not
// negotiate an API version, and the readiness probe in particular is
// named in deployment manifests — versioning it would mean re-editing
// those every time the API version moves.
func TestIntegration_Versioning_ContractIsUnderV1(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, attachmentConfig(t), discardLogger()).Handler)
	defer srv.Close()

	tests := []struct {
		name string
		path string
		// versioned routes answer 401 unauthenticated, which is proof
		// the route exists; 404 would mean it does not.
		wantVersioned   int
		wantUnversioned int
	}{
		{"tasks", "/tasks", http.StatusUnauthorized, http.StatusNotFound},
		{"one task", "/tasks/some-id", http.StatusUnauthorized, http.StatusNotFound},
		{"attachments", "/tasks/some-id/attachments", http.StatusUnauthorized, http.StatusNotFound},
		{"files", "/files/some-key", http.StatusUnauthorized, http.StatusNotFound},
		{"auth me", "/auth/me", http.StatusUnauthorized, http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			versioned, err := srv.Client().Get(srv.URL + apiPrefix + tt.path)
			if err != nil {
				t.Fatalf("GET %s%s: %v", apiPrefix, tt.path, err)
			}
			versioned.Body.Close()
			if versioned.StatusCode != tt.wantVersioned {
				t.Errorf("GET %s%s = %d, want %d", apiPrefix, tt.path, versioned.StatusCode, tt.wantVersioned)
			}

			// The unversioned path must be gone, not quietly still
			// served. A dual mount would make the version prefix
			// decorative and let a client keep depending on a contract
			// nobody is versioning.
			unversioned, err := srv.Client().Get(srv.URL + tt.path)
			if err != nil {
				t.Fatalf("GET %s: %v", tt.path, err)
			}
			unversioned.Body.Close()
			if unversioned.StatusCode != tt.wantUnversioned {
				t.Errorf("GET %s = %d, want %d — the unprefixed contract must not still be served",
					tt.path, unversioned.StatusCode, tt.wantUnversioned)
			}
		})
	}
}

// TestIntegration_Versioning_OperationalRoutesStayUnprefixed is the other
// half: these must keep working exactly where they were, and must *not*
// have gained a /v1 alias.
func TestIntegration_Versioning_OperationalRoutesStayUnprefixed(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, testConfig(), discardLogger()).Handler)
	defer srv.Close()

	for _, path := range []string{"/health", "/health/ready"} {
		t.Run(path, func(t *testing.T) {
			resp, err := srv.Client().Get(srv.URL + path)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
			}

			prefixed, err := srv.Client().Get(srv.URL + apiPrefix + path)
			if err != nil {
				t.Fatalf("GET %s%s: %v", apiPrefix, path, err)
			}
			prefixed.Body.Close()
			if prefixed.StatusCode != http.StatusNotFound {
				t.Errorf("GET %s%s = %d, want 404 — operational routes are not part of the versioned contract",
					apiPrefix, path, prefixed.StatusCode)
			}
		})
	}
}

// TestIntegration_Versioning_UnknownPathUnderV1Is404 guards the subtree
// mount: "/v1/" catches everything beneath it, so an unknown path must
// 404 from inside the versioned mux rather than falling through to
// something else.
func TestIntegration_Versioning_UnknownPathUnderV1Is404(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, testConfig(), discardLogger()).Handler)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + apiPrefix + "/no-such-thing")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET %s/no-such-thing = %d, want 404", apiPrefix, resp.StatusCode)
	}
}

// --- cross-origin end to end ---

// TestIntegration_CrossOriginFlow walks the whole sequence a browser
// frontend performs, from an origin other than the API's: register, log
// in, keep the token, create a task with it, list tasks back.
//
// The CORS tests elsewhere in this file assert headers on single
// requests. This one asserts the *flow* — that a client which only ever
// sees what CORS exposes to it can actually complete the sequence. The
// two are different failures: a preflight can be answered correctly and
// the flow still break because a header the client needs is not on the
// exposed list.
//
// Note there is no CSRF step. Auth is a bearer token in a header the
// frontend sets itself, not a cookie the browser attaches automatically,
// so the vector CSRF defends against does not exist here (see
// docs/DECISIONS.md).
func TestIntegration_CrossOriginFlow(t *testing.T) {
	const frontendOrigin = "http://localhost:3000"

	cfg := testConfig()
	cfg.CORSAllowedOrigins = []string{frontendOrigin}

	srv := httptest.NewServer(newTestServer(t, cfg, discardLogger()).Handler)
	defer srv.Close()

	// A browser sends Origin on every cross-origin request, and refuses
	// to expose the response to the script unless the reply allows it.
	// Every request below carries it, and every response is checked.
	do := func(t *testing.T, method, path, token, body string) *http.Response {
		t.Helper()

		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, srv.URL+apiPrefix+path, reader)
		if err != nil {
			t.Fatalf("build %s %s: %v", method, path, err)
		}
		req.Header.Set("Origin", frontendOrigin)
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
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != frontendOrigin {
			t.Errorf("%s %s: Access-Control-Allow-Origin = %q, want %q — a browser would not expose this response to the caller",
				method, path, got, frontendOrigin)
		}
		return resp
	}

	// A real browser preflights the authenticated, JSON-bodied POST
	// before sending it, because Authorization and Content-Type are not
	// CORS-safelisted request headers.
	preflight, err := http.NewRequest(http.MethodOptions, srv.URL+apiPrefix+"/tasks", nil)
	if err != nil {
		t.Fatalf("build preflight: %v", err)
	}
	preflight.Header.Set("Origin", frontendOrigin)
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "authorization,content-type")

	preflightResp, err := srv.Client().Do(preflight)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	preflightResp.Body.Close()
	if preflightResp.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", preflightResp.StatusCode)
	}
	allowedHeaders := strings.ToLower(preflightResp.Header.Get("Access-Control-Allow-Headers"))
	if !strings.Contains(allowedHeaders, "authorization") {
		t.Fatalf("preflight Access-Control-Allow-Headers = %q, want it to permit Authorization — without it the browser never sends the token",
			preflightResp.Header.Get("Access-Control-Allow-Headers"))
	}

	// 1. Register.
	const credentials = `{"email":"frontend@example.com","password":"password12345"}`
	regResp := do(t, http.MethodPost, "/auth/register", "", credentials)
	regResp.Body.Close()
	if regResp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, want 201", regResp.StatusCode)
	}

	// 2. Log in and keep the token.
	loginResp := do(t, http.MethodPost, "/auth/login", "", credentials)
	defer loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", loginResp.StatusCode)
	}
	var login struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&login); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if login.Token == "" {
		t.Fatal("login returned an empty token")
	}

	// 3. Create a task with the token.
	createResp := do(t, http.MethodPost, "/tasks", login.Token, `{"title":"from the frontend"}`)
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create task status = %d, want 201", createResp.StatusCode)
	}
	var created struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created task: %v", err)
	}

	// 4. List it back.
	listResp := do(t, http.MethodGet, "/tasks", login.Token, "")
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listResp.StatusCode)
	}
	var listed []struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Errorf("list returned %+v, want exactly the task just created (%s)", listed, created.ID)
	}
}
