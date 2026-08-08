package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// discardLogger returns a logger that silently discards all output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
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
