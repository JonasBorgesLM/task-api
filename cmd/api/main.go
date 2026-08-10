package main

import (
	"context"
	"database/sql"
	"errors"
	"expvar"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/JonasBorgesLM/task-api/config"
	"github.com/JonasBorgesLM/task-api/middleware"
	"github.com/JonasBorgesLM/task-api/task"

	// Registers the "pgx" driver with database/sql under the name used by
	// sql.Open below. This is the only file in the module that imports a
	// PostgreSQL package directly — task.Repository, task.Service and
	// task.Handler know nothing about PostgreSQL or database/sql; they
	// depend only on the task.Repository interface (see
	// buildRepository).
	_ "github.com/jackc/pgx/v5/stdlib"
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

	srv, closeRepo, err := newServer(cfg, logger)
	if err != nil {
		return fmt.Errorf("build server: %w", err)
	}
	// Runs last, after the HTTP server has fully stopped serving requests
	// (Shutdown below, and the serverErr drain that follows it) — closing
	// the repository (e.g. the PostgreSQL connection pool) out from under
	// a request still in flight would turn a clean shutdown into request
	// failures for no reason.
	defer func() {
		if err := closeRepo(); err != nil {
			logger.Error("failed to close repository", "error", err)
		}
	}()

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
//
// The returned close function releases whatever resources buildRepository
// opened (a PostgreSQL connection pool, or nothing for the in-memory
// store) and must be called exactly once, after the server is done
// serving requests. newServer returns a non-nil error only if the
// Repository itself could not be built (e.g. PostgreSQL is configured but
// unreachable) — in that case both other return values are nil and there
// is nothing to close.
func newServer(cfg config.Config, logger *slog.Logger) (*http.Server, func() error, error) {
	repo, closeRepo, err := buildRepository(cfg, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("build repository: %w", err)
	}

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
	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      rootHandler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	return srv, closeRepo, nil
}

// buildRepository is the single place that decides which task.Repository
// implementation backs the application, so that decision never leaks into
// task.Service or task.Handler — both depend only on the task.Repository
// interface (see task/repository.go) and are unaware PostgreSQL exists.
//
// When cfg.DatabaseURL is empty — the zero value, which is what every
// existing test that builds a bare config.Config{} gets — it returns the
// in-memory implementation, unchanged from before PostgreSQL support was
// added. Otherwise it opens a PostgreSQL connection pool, applies pending
// migrations (unless cfg.DBAutoMigrate is false), and returns a
// PostgreSQL-backed Repository.
//
// The returned close function must be called exactly once, after the
// server has stopped serving requests, to release the connection pool (a
// no-op for the in-memory store).
func buildRepository(cfg config.Config, logger *slog.Logger) (task.Repository, func() error, error) {
	if cfg.DatabaseURL == "" {
		return task.NewMemoryRepository(), func() error { return nil }, nil
	}

	// "pgx" is the driver name github.com/jackc/pgx/v5/stdlib registers
	// with database/sql on import — see the blank import at the top of
	// this file.
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)
	db.SetConnMaxLifetime(cfg.DBConnMaxLifetime)

	// sql.Open never actually dials the database — it only validates the
	// DSN and prepares the pool lazily. Ping forces a real connection now,
	// so a misconfigured DATABASE_URL fails startup immediately with a
	// clear error instead of surfacing later as a mysterious failure on
	// the first request.
	pingCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("ping database: %w", err)
	}

	if cfg.DBAutoMigrate {
		migrateCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := task.RunMigrations(migrateCtx, db); err != nil {
			db.Close()
			return nil, nil, fmt.Errorf("run migrations: %w", err)
		}
		logger.Info("database migrations applied")
	}

	return task.NewPostgresRepository(db), db.Close, nil
}
