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

	srv, closeDB, err := newServer(ctx, cfg, discardLogger())
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

	srv, closeDB, err := newServer(ctx, cfg, discardLogger())
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
