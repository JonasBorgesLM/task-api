package main

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/JonasBorgesLM/task-api/config"
	"github.com/JonasBorgesLM/task-api/middleware"
	"github.com/JonasBorgesLM/task-api/task"
)

func main() {
	// ctx is canceled the moment SIGINT or SIGTERM is received; run() uses
	// that cancellation as the trigger to start shutting down.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Stdout); err != nil {
		// run() already logs the specifics of most failures itself, at the
		// configured level, as they happen. This is only a last-resort line
		// for the case run() can't: a failure before a leveled logger even
		// exists (e.g. config.Load itself rejecting a bad env var).
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("fatal error", "error", err)
		os.Exit(1)
	}
}

// run wires dependencies, starts the HTTP server, and handles graceful
// shutdown. It returns a non-nil error only for unexpected failures — a
// clean shutdown (triggered by ctx being canceled) returns nil. Structured
// JSON logs are written to out, at the level configured by LOG_LEVEL.
func run(ctx context.Context, out io.Writer) error {
	// Load configuration from environment variables.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: cfg.LogLevel}))

	srv := newServer(cfg, logger)

	// serverErr receives the result of ListenAndServe from its goroutine.
	// The server keeps serving requests normally until either it fails on
	// its own or ctx is canceled below — nothing else touches it meanwhile.
	serverErr := make(chan error, 1)

	go func() {
		logger.Info("server started", "addr", cfg.Addr)
		serverErr <- srv.ListenAndServe()
	}()

	// Block until a termination signal cancels ctx or the server fails on
	// its own (e.g. the port is already in use).
	select {
	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)

	case <-ctx.Done():
		logger.Info("signal received", "reason", context.Cause(ctx))
	}

	logger.Info("shutdown initiated", "timeout", cfg.ShutdownTimeout.String())

	// A fresh, un-canceled context bounds how long Shutdown may block
	// waiting for in-flight requests — ctx itself is already canceled at
	// this point, so it cannot be reused here.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "error", err)
		return fmt.Errorf("shutdown: %w", err)
	}

	// Drain the serverErr channel: after Shutdown returns, ListenAndServe
	// will have returned http.ErrServerClosed, which is the expected
	// outcome and not itself an error worth reporting.
	if err := <-serverErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server error: %w", err)
	}

	logger.Info("shutdown completed")
	return nil
}

// newServer builds the fully wired *http.Server described by cfg:
// dependency wiring (Repository → Service → Handler), route registration,
// and the cross-cutting middleware chain. It is used by run() and, so that
// tests exercise the exact same wiring instead of re-deriving their own
// copy of it, by tests that need a Handler without going through
// ListenAndServe/Shutdown.
func newServer(cfg config.Config, logger *slog.Logger) *http.Server {
	repo := task.NewMemoryRepository()
	svc := task.NewService(repo)
	handler := task.NewHandler(svc, logger)

	mux := http.NewServeMux()
	registerHealthRoute(mux, logger)
	mux.Handle("GET /debug/vars", expvar.Handler())
	handler.RegisterRoutes(mux)

	// Cross-cutting HTTP concerns, applied to every route. Order matters:
	// RequestID must run first so Logging and Recovery can read the
	// request ID from the request context, and Logging must wrap Recovery
	// (not the other way around) so it still logs an accurate status code
	// for requests that panicked and were recovered.
	rootHandler := middleware.Chain(
		middleware.RequestID,
		middleware.Logging(logger),
		middleware.Recovery(logger),
	)(mux)

	// HTTP server with explicit timeouts, sourced entirely from Config.
	return &http.Server{
		Addr:         cfg.Addr,
		Handler:      rootHandler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
}
