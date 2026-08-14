package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// discardLogger returns a logger that silently discards all output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// syncBuffer is a concurrency-safe io.Writer / fmt.Stringer, used to
// capture run()'s log output from tests: run() logs from its own
// goroutine (the one running srv.ListenAndServe/logger.Info("server
// started", ...)) while the test concurrently polls the buffer, and a bare
// bytes.Buffer is not safe for that.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// waitForLogLine polls buf (up to a generous timeout) until it contains
// substr. Used to deterministically know that run()'s server goroutine has
// reached a given point (e.g. logged "server started") without hardcoding
// an arbitrary sleep duration and racing it against the goroutine.
func waitForLogLine(t *testing.T, buf *syncBuffer, substr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), substr) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for a log line containing %q; got: %q", substr, buf.String())
}

// freeAddr returns a "127.0.0.1:<port>" address for a port that is free at
// the moment this function returns. config.Load rejects HTTP_ADDR=":0"
// (port 0 is explicitly out of the valid 1–65535 range — see
// config.validateAddr), so run() can never be told to bind an
// OS-assigned port directly; this grab-and-release is the standard
// workaround for tests that need a real, otherwise-arbitrary port. There
// is an inherent, unavoidable TOCTOU race (another process could take the
// port between Close and run()'s own Listen), but it's negligible for a
// local test run.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find a free port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("failed to close probe listener: %v", err)
	}
	return addr
}

// startTestServer starts a real HTTP server on a random port and returns the
// server, its address, and a channel that receives the ListenAndServe result.
func startTestServer(t *testing.T, handler http.Handler) (*http.Server, string, <-chan error) {
	t.Helper()

	// Port :0 asks the kernel for any available port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	srv := &http.Server{Handler: handler}
	errCh := make(chan error, 1)

	go func() {
		errCh <- srv.Serve(ln)
	}()

	return srv, ln.Addr().String(), errCh
}

// TestServer_RespondsBeforeShutdown verifies that the server handles requests normally.
func TestServer_RespondsBeforeShutdown(t *testing.T) {
	mux := http.NewServeMux()
	registerHealthRoute(mux, discardLogger())

	srv, addr, _ := startTestServer(t, mux)
	defer srv.Shutdown(context.Background()) //nolint:errcheck

	resp, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// TestServer_ShutdownStopsAcceptingConnections verifies that after Shutdown,
// the server refuses new connections.
func TestServer_ShutdownStopsAcceptingConnections(t *testing.T) {
	mux := http.NewServeMux()
	registerHealthRoute(mux, discardLogger())

	srv, addr, errCh := startTestServer(t, mux)

	// Clean shutdown with generous timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error: %v", err)
	}

	// ListenAndServe must have returned http.ErrServerClosed.
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		t.Errorf("server error after shutdown = %v, want http.ErrServerClosed", err)
	}

	// New connections should now be refused.
	_, err := http.Get("http://" + addr + "/health")
	if err == nil {
		t.Error("expected connection refused after shutdown, got nil error")
	}
}

// TestServer_ShutdownWaitsForInflightRequests verifies that an in-flight request
// completes before Shutdown returns.
func TestServer_ShutdownWaitsForInflightRequests(t *testing.T) {
	const handlerDelay = 100 * time.Millisecond

	requestStarted := make(chan struct{})
	requestDone := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted) // signal that the handler is running
		time.Sleep(handlerDelay)
		w.WriteHeader(http.StatusOK)
		close(requestDone) // signal that the handler completed
	})

	srv, addr, errCh := startTestServer(t, mux)

	// Fire a request in the background.
	go func() { http.Get("http://" + addr + "/slow") }() //nolint:errcheck

	// Wait until the handler has started before triggering shutdown.
	<-requestStarted

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error: %v", err)
	}

	// The handler must have completed before Shutdown returned.
	select {
	case <-requestDone:
		// expected: handler finished before shutdown
	default:
		t.Error("Shutdown() returned before in-flight request completed")
	}

	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		t.Errorf("server error = %v, want http.ErrServerClosed", err)
	}
}

// TestServer_ShutdownTimeout verifies that Shutdown returns an error when
// the context deadline is exceeded while requests are still in-flight.
func TestServer_ShutdownTimeout(t *testing.T) {
	requestStarted := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /forever", func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		// Block until the request context is cancelled (server shutdown).
		<-r.Context().Done()
	})

	srv, addr, _ := startTestServer(t, mux)

	go func() { http.Get("http://" + addr + "/forever") }() //nolint:errcheck

	<-requestStarted

	// Extremely short timeout — will expire before the handler finishes.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	err := srv.Shutdown(ctx)
	if err == nil {
		t.Error("Shutdown() expected error on timeout, got nil")
	}
}

// --- Health check ---

// TestHealthEndpoint verifies status code, Content-Type and response body.
func TestHealthEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	registerHealthRoute(mux, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /health status = %d, want %d", w.Code, http.StatusOK)
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("GET /health Content-Type = %q, want application/json", ct)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode health response: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("GET /health body status = %q, want %q", body["status"], "ok")
	}
}

// --- Readiness check ---

// TestReadinessEndpoint_NilDB_AlwaysReady verifies that the in-memory-store
// configuration (db == nil, see openDatabase) is always reported ready —
// there's nothing external to check.
func TestReadinessEndpoint_NilDB_AlwaysReady(t *testing.T) {
	mux := http.NewServeMux()
	registerReadinessRoute(mux, nil, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /health/ready status = %d, want %d", w.Code, http.StatusOK)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode readiness response: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("GET /health/ready body status = %q, want %q", body["status"], "ok")
	}
}

// TestReadinessEndpoint_UnreachableDB_ReturnsServiceUnavailable verifies
// that a *sql.DB whose Ping fails makes GET /health/ready report 503
// instead of the always-200 GET /health — this is the whole point of the
// readiness check: an orchestrator can act on this distinction to stop
// routing traffic to this replica.
//
// A closed *sql.DB is used to get a deterministic Ping failure ("sql:
// database is closed") without needing a real, unreachable PostgreSQL
// instance: sql.Open never dials anything (see openDatabase's doc
// comment), so opening then immediately closing a pool needs no network
// access at all and keeps this a unit test.
func TestReadinessEndpoint_UnreachableDB_ReturnsServiceUnavailable(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://unreachable/db")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	mux := http.NewServeMux()
	registerReadinessRoute(mux, db, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /health/ready status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode readiness response: %v", err)
	}
	if body["status"] != "unavailable" {
		t.Errorf("GET /health/ready body status = %q, want %q", body["status"], "unavailable")
	}
}

// --- run() ---
//
// Unlike the tests above (which exercise individual pieces run() is built
// from — registerHealthRoute, http.Server.Shutdown semantics), these call
// run() itself: the same function main() calls, with the same config
// loading, dependency wiring (via newServer) and signal-driven shutdown
// path. This closes the gap where a regression inside run() itself (e.g.
// the wrong shutdown order, or config.Load never actually being called)
// could go undetected even though its constituent pieces are each well
// tested in isolation.

// TestRun_GracefulShutdownOnContextCancel verifies that run(), driven by a
// context cancellation exactly as main() drives it from a SIGINT/SIGTERM,
// starts the server, shuts it down cleanly and returns nil.
func TestRun_GracefulShutdownOnContextCancel(t *testing.T) {
	t.Setenv("HTTP_ADDR", freeAddr(t))
	t.Setenv("HTTP_SHUTDOWN_TIMEOUT", "5s")

	out := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)
	go func() { runErr <- run(ctx, out) }()

	waitForLogLine(t, out, "server started")

	cancel()

	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("run() error = %v, want nil after a clean, signal-driven shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() did not return within 5s of ctx cancellation")
	}

	if !strings.Contains(out.String(), "shutdown completed") {
		t.Errorf(`expected a "shutdown completed" log line, got: %s`, out.String())
	}
}

// TestRun_InvalidConfig_ReturnsError verifies that run() surfaces a
// config.Load failure as an error instead of e.g. panicking or silently
// falling back to defaults.
func TestRun_InvalidConfig_ReturnsError(t *testing.T) {
	t.Setenv("HTTP_ADDR", "not-a-valid-address")

	if err := run(context.Background(), io.Discard); err == nil {
		t.Error("run() with an invalid HTTP_ADDR: expected an error, got nil")
	}
}

// TestRun_AddrAlreadyInUse_ReturnsError verifies that run() surfaces
// ListenAndServe failing on its own (e.g. the port is already bound by
// another process) as an error, via the `case err := <-serverErr` branch
// — distinct from the signal-driven shutdown path exercised by
// TestRun_GracefulShutdownOnContextCancel.
func TestRun_AddrAlreadyInUse_ReturnsError(t *testing.T) {
	addr := freeAddr(t)

	// Occupy the port for the whole test so run()'s own ListenAndServe
	// fails on it.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to occupy %s: %v", addr, err)
	}
	defer ln.Close()

	t.Setenv("HTTP_ADDR", addr)

	runErr := make(chan error, 1)
	go func() { runErr <- run(context.Background(), io.Discard) }()

	select {
	case err := <-runErr:
		if err == nil {
			t.Error("run() with an already-occupied HTTP_ADDR: expected an error, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() did not return within 5s for an already-occupied HTTP_ADDR")
	}
}

// TestRun_UsesConfiguredLogLevel verifies that LOG_LEVEL actually reaches
// the logger run() builds and uses for the server: setting it to "error"
// must suppress the Info-level "server started" line.
func TestRun_UsesConfiguredLogLevel(t *testing.T) {
	t.Setenv("HTTP_ADDR", freeAddr(t))
	t.Setenv("LOG_LEVEL", "error")

	out := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)
	go func() { runErr <- run(ctx, out) }()

	// LOG_LEVEL=error suppresses the one Info-level line
	// (waitForLogLine's usual synchronization signal in the test above),
	// so there is nothing to poll for here. A short fixed wait is used
	// instead, purely to give the server goroutine a chance to run before
	// cancellation — correctness doesn't depend on its exact duration,
	// only on it being enough for a goroutine switch on a local listener.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("run() error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() did not return within 5s of ctx cancellation")
	}

	if strings.Contains(out.String(), "server started") {
		t.Error(`LOG_LEVEL=error must suppress the Info-level "server started" log line`)
	}
}
