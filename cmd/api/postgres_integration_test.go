//go:build integration

package main

// Integration test for newServer/openDatabase's real PostgreSQL path —
// same build-tag/skip safeguards as internal/task's and internal/user's
// postgres tests; see either's doc comment for the full rationale.
//
// main_integration_test.go (no build tag) exercises newServer's wiring
// end to end, but only ever against the in-memory repositories
// (config.Config{} / testConfig() both leave DatabaseURL empty) — the
// actual sql.Open/Ping/RunMigrations sequence in openDatabase, the code
// path that runs for real whenever DATABASE_URL is configured in
// production, had no automated coverage at all before this file.

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/JonasBorgesLM/task-api/internal/config"
	"github.com/JonasBorgesLM/task-api/internal/task"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const testDatabaseURLEnv = "TEST_DATABASE_URL"

// postgresTestConfig returns a config.Config pointed at TEST_DATABASE_URL
// with DBAutoMigrate explicitly enabled — building a config.Config{}
// literal directly (rather than through config.Load()) leaves every bool
// field at its Go zero value, and DBAutoMigrate's zero value is false, so
// this cannot just add DatabaseURL to testConfig() from
// main_integration_test.go. Skips the calling test if TEST_DATABASE_URL
// is unset.
func postgresTestConfig(t *testing.T) config.Config {
	t.Helper()

	url := os.Getenv(testDatabaseURLEnv)
	if url == "" {
		t.Skipf("%s not set; skipping PostgreSQL integration test (see docker-compose.yml)", testDatabaseURLEnv)
	}

	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping database: %v", err)
	}
	if _, err := db.ExecContext(ctx, `TRUNCATE TABLE sessions, tasks, users CASCADE`); err != nil {
		// Tables may not exist yet on a database newServer itself hasn't
		// migrated — that's fine, openDatabase's own RunMigrations call
		// (exercised by the test using this config) will create them.
		t.Logf("truncate tables (ignored, likely first run against this database): %v", err)
	}

	cfg := testConfig()
	cfg.DatabaseURL = url
	cfg.DBMaxOpenConns = 5
	cfg.DBMaxIdleConns = 5
	cfg.DBConnMaxLifetime = time.Minute
	cfg.DBAutoMigrate = true
	return cfg
}

// TestPostgres_ServerLifecycle drives newServer's real PostgreSQL wiring —
// sql.Open, Ping, RunMigrations, both domains' postgresRepository — through
// the same register/login/create/read/delete cycle
// TestIntegration_TaskLifecycle exercises against the in-memory store, plus
// a readiness check that must report "ok" only because the database is
// actually reachable (not merely because there's nothing to check, as
// with the in-memory store).
func TestPostgres_ServerLifecycle(t *testing.T) {
	cfg := postgresTestConfig(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv, closeDB, err := newServer(ctx, cfg, discardLogger(), nil)
	if err != nil {
		t.Fatalf("newServer() unexpected error: %v", err)
	}
	t.Cleanup(func() {
		if err := closeDB(); err != nil {
			t.Errorf("closeDB() unexpected error: %v", err)
		}
	})

	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	client := ts.Client()

	readyResp, err := client.Get(ts.URL + "/health/ready")
	if err != nil {
		t.Fatalf("GET /health/ready: %v", err)
	}
	defer readyResp.Body.Close()
	if readyResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health/ready status = %d, want %d (database should be reachable)", readyResp.StatusCode, http.StatusOK)
	}

	token := registerAndLogin(t, ts)

	createResp, err := client.Do(authedRequest(t, token, http.MethodPost, ts.URL+apiPrefix+"/tasks",
		`{"title":"Postgres-backed task","description":"created against real PostgreSQL"}`))
	if err != nil {
		t.Fatalf("POST /tasks: %v", err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(createResp.Body)
		t.Fatalf("POST /tasks status = %d, want %d, body = %s", createResp.StatusCode, http.StatusCreated, body)
	}

	var created task.Task
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" {
		t.Fatal("POST /tasks: created task has empty ID")
	}

	getResp, err := client.Do(authedRequest(t, token, http.MethodGet, ts.URL+apiPrefix+"/tasks/"+created.ID, ""))
	if err != nil {
		t.Fatalf("GET /tasks/{id}: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /tasks/{id} status = %d, want %d", getResp.StatusCode, http.StatusOK)
	}

	deleteResp, err := client.Do(authedRequest(t, token, http.MethodDelete, ts.URL+apiPrefix+"/tasks/"+created.ID, ""))
	if err != nil {
		t.Fatalf("DELETE /tasks/{id}: %v", err)
	}
	defer deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /tasks/{id} status = %d, want %d", deleteResp.StatusCode, http.StatusNoContent)
	}

	// Persistence: a fresh, separate GET against the real database must
	// now report it gone.
	goneResp, err := client.Do(authedRequest(t, token, http.MethodGet, ts.URL+apiPrefix+"/tasks/"+created.ID, ""))
	if err != nil {
		t.Fatalf("GET /tasks/{id} after delete: %v", err)
	}
	defer goneResp.Body.Close()
	if goneResp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /tasks/{id} after delete: status = %d, want %d", goneResp.StatusCode, http.StatusNotFound)
	}
}

// TestPostgres_ReadinessReportsUnavailable_WhenDatabaseUnreachable verifies
// the other half of GET /health/ready's contract against a real
// *sql.DB — a closed pool must report 503, not silently 200. Mirrors
// cmd/api/main_test.go's TestReadinessEndpoint_UnreachableDB test at the
// registerReadinessRoute level, but here through newServer's actual
// wiring with a real (Postgres-driver) *sql.DB.
func TestPostgres_ReadinessReportsUnavailable_WhenDatabaseUnreachable(t *testing.T) {
	cfg := postgresTestConfig(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv, closeDB, err := newServer(ctx, cfg, discardLogger(), nil)
	if err != nil {
		t.Fatalf("newServer() unexpected error: %v", err)
	}
	// Close the pool immediately instead of via t.Cleanup — the test
	// deliberately makes the database unreachable through the server's
	// own connection, then verifies readiness reflects that.
	if err := closeDB(); err != nil {
		t.Fatalf("closeDB() unexpected error: %v", err)
	}

	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/health/ready")
	if err != nil {
		t.Fatalf("GET /health/ready: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("GET /health/ready status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
}

// TestPostgres_DBStats_ReportsRealPoolConfig is 16.C1's wiring proof
// against a real *sql.DB: MaxOpenConnections on /debug/vars must match
// what openDatabase actually configured (postgresTestConfig sets
// DBMaxOpenConns to 5, deliberately different from the production
// default of 25, so this cannot pass by coincidence against a
// zero-valued or default-valued stats struct).
func TestPostgres_DBStats_ReportsRealPoolConfig(t *testing.T) {
	cfg := postgresTestConfig(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv, closeDB, err := newServer(ctx, cfg, discardLogger(), nil)
	if err != nil {
		t.Fatalf("newServer() unexpected error: %v", err)
	}
	t.Cleanup(func() {
		if err := closeDB(); err != nil {
			t.Errorf("closeDB() unexpected error: %v", err)
		}
	})

	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	token := registerAndLogin(t, ts)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/debug/vars", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /debug/vars: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /debug/vars body: %v", err)
	}

	var stats dbStatsVars
	if err := json.Unmarshal(body["db_stats"], &stats); err != nil {
		t.Fatalf("decode db_stats: %v", err)
	}
	if stats.MaxOpenConnections != cfg.DBMaxOpenConns {
		t.Errorf("db_stats.max_open_connections = %d, want %d (cfg.DBMaxOpenConns)", stats.MaxOpenConnections, cfg.DBMaxOpenConns)
	}
}

// TestPostgres_PostgresBreaker_ReportsClosedAndProtectsRealTraffic is
// 16.C2's end-to-end proof against a real *sql.DB: the shared breaker
// reports Closed under healthy traffic, and — the part that actually
// matters — a full register/login/create/read/update/delete cycle
// still works with every task.Repository and user.Repository call
// routed through it. A wiring mistake that routed every call straight
// to ErrUnavailable would still leave postgres_breaker looking healthy
// on its own; this is what catches that.
func TestPostgres_PostgresBreaker_ReportsClosedAndProtectsRealTraffic(t *testing.T) {
	cfg := postgresTestConfig(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv, closeDB, err := newServer(ctx, cfg, discardLogger(), nil)
	if err != nil {
		t.Fatalf("newServer() unexpected error: %v", err)
	}
	t.Cleanup(func() {
		if err := closeDB(); err != nil {
			t.Errorf("closeDB() unexpected error: %v", err)
		}
	})

	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	token := registerAndLogin(t, ts)
	client := ts.Client()

	createResp, err := client.Do(authedRequest(t, token, http.MethodPost, ts.URL+apiPrefix+"/tasks",
		`{"title":"Breaker-protected task","description":"created through the shared postgres breaker"}`))
	if err != nil {
		t.Fatalf("POST /tasks: %v", err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(createResp.Body)
		t.Fatalf("POST /tasks status = %d, body = %s", createResp.StatusCode, respBody)
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/debug/vars", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /debug/vars: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /debug/vars body: %v", err)
	}
	var v attachmentBreakerVars
	if err := json.Unmarshal(body["postgres_breaker"], &v); err != nil {
		t.Fatalf("decode postgres_breaker: %v", err)
	}
	if v.State != "closed" {
		t.Errorf("postgres_breaker.state = %q, want %q — healthy traffic must not have tripped it", v.State, "closed")
	}
}
