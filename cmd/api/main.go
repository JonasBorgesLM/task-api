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

	"github.com/JonasBorgesLM/task-api/internal/config"
	"github.com/JonasBorgesLM/task-api/internal/middleware"
	"github.com/JonasBorgesLM/task-api/internal/platform/migrate"
	"github.com/JonasBorgesLM/task-api/internal/task"
	"github.com/JonasBorgesLM/task-api/internal/user"

	// Registers the "pgx" driver with database/sql under the name used by
	// sql.Open below. This is the only file in the module that imports a
	// PostgreSQL package directly — task.Repository, user.Repository and
	// every Service/Handler know nothing about PostgreSQL or database/sql;
	// they depend only on their own package's Repository interface (see
	// openDatabase and newServer).
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

	srv, closeDB, err := newServer(ctx, cfg, logger)
	if err != nil {
		return fmt.Errorf("build server: %w", err)
	}
	// Runs last, after the HTTP server has fully stopped serving requests
	// (Shutdown below, and the serverErr drain that follows it) — closing
	// the database out from under a request still in flight would turn a
	// clean shutdown into request failures for no reason.
	defer func() {
		if err := closeDB(); err != nil {
			logger.Error("failed to close database", "error", err)
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
// dependency wiring (Repository → Service → Handler for both task and
// user), route registration, and the cross-cutting middleware chain. It is
// used by run() and, so that tests exercise the exact same wiring instead
// of re-deriving their own copy of it, by tests that need a Handler
// without going through ListenAndServe/Shutdown.
//
// ctx bounds the lifetime of the background maintenance goroutine started
// here (see runPeriodicCleanup) — it stops the moment ctx is canceled,
// same as the HTTP server itself is meant to.
//
// The returned close function releases whatever openDatabase opened (a
// PostgreSQL connection pool, or nothing for the in-memory store) and must
// be called exactly once, after the server is done serving requests.
// newServer returns a non-nil error only if the database itself could not
// be opened (e.g. PostgreSQL is configured but unreachable) — in that case
// both other return values are nil and there is nothing to close.
func newServer(ctx context.Context, cfg config.Config, logger *slog.Logger) (*http.Server, func() error, error) {
	db, err := openDatabase(cfg, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	closeDB := func() error { return nil }
	if db != nil {
		closeDB = db.Close
	}

	// One *sql.DB backs both domains' postgresRepository — task and user
	// live in the same database, so there is no reason to open two pools.
	// db == nil (cfg.DatabaseURL unset) selects the in-memory
	// implementation for both, unchanged from before this second domain
	// existed.
	var (
		taskRepo task.Repository
		userRepo user.Repository
	)
	if db == nil {
		taskRepo = task.NewMemoryRepository()
		userRepo = user.NewMemoryRepository()
	} else {
		taskRepo = task.NewPostgresRepository(db)
		userRepo = user.NewPostgresRepository(db)
	}

	taskSvc := task.NewService(taskRepo)
	userSvc := user.NewService(userRepo, cfg.AuthSessionTTL)

	taskHandler := task.NewHandler(taskSvc, logger)
	userHandler := user.NewHandler(userSvc, logger)
	requireAuth := user.RequireAuth(userSvc, logger)

	// authRateLimiter guards only /auth/register and /auth/login (see
	// userHandler.RegisterRoutes below) — it exists to blunt
	// credential-stuffing and registration-spam attempts against those two
	// endpoints specifically, not as a general-purpose API rate limit.
	authRateLimiter := middleware.NewRateLimiter(authRateLimitRequests, authRateLimitWindow)

	mux := http.NewServeMux()
	registerHealthRoute(mux, logger)
	registerReadinessRoute(mux, db, logger)

	// /debug/vars is authenticated, unlike the two health routes above.
	// expvar exposes the process's command line and full runtime/GC
	// statistics — internal operational detail with no reason to be
	// world-readable, and a free fingerprint of the deployment for anyone
	// probing it. The health routes stay public because an orchestrator's
	// probe has no credentials to offer; expvar has no such constraint,
	// since the humans and scrapers who read it can carry a token.
	mux.Handle("GET /debug/vars", requireAuth(expvar.Handler()))

	userHandler.RegisterRoutes(mux, requireAuth, authRateLimiter.Middleware())
	taskHandler.RegisterRoutes(mux, requireAuth)

	go runPeriodicCleanup(ctx, userSvc, authRateLimiter, logger)

	// Cross-cutting HTTP concerns, applied to every route. Order matters:
	// RequestID must run first so Logging and Recovery can read the
	// request ID from the request context, and Logging must wrap Recovery
	// (not the other way around) so it still logs an accurate status code
	// for requests that panicked and were recovered. CORS sits between
	// them, inside Logging (so a preflight request it answers directly
	// still gets an access log line) but outside Recovery (so the
	// Access-Control-Allow-Origin header it sets survives even a handler
	// panic — Recovery's 500 doesn't clear headers set earlier in the
	// chain). It is a no-op end to end unless CORS_ALLOWED_ORIGINS is set;
	// see middleware.CORS.
	rootHandler := middleware.Chain(
		middleware.RequestID,
		middleware.Logging(logger),
		middleware.CORS(cfg.CORSAllowedOrigins),
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

	return srv, closeDB, nil
}

// openDatabase is the single place that decides whether the application
// runs against PostgreSQL or the in-memory store, so that decision never
// leaks into task.Service/task.Handler or user.Service/user.Handler — all
// of them depend only on their own package's Repository interface and are
// unaware PostgreSQL exists.
//
// When cfg.DatabaseURL is empty — the zero value, which is what every
// existing test that builds a bare config.Config{} gets — it returns a nil
// *sql.DB and a nil error, which newServer reads as "use the in-memory
// repositories". Otherwise it opens a PostgreSQL connection pool, applies
// pending migrations (unless cfg.DBAutoMigrate is false), and returns the
// pool.
//
// The caller must Close the returned *sql.DB (when non-nil) exactly once,
// after the server has stopped serving requests.
func openDatabase(cfg config.Config, logger *slog.Logger) (*sql.DB, error) {
	if cfg.DatabaseURL == "" {
		return nil, nil
	}

	// "pgx" is the driver name github.com/jackc/pgx/v5/stdlib registers
	// with database/sql on import — see the blank import at the top of
	// this file.
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
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
		return nil, fmt.Errorf("ping database: %w", err)
	}

	if cfg.DBAutoMigrate {
		migrateCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := migrate.RunMigrations(migrateCtx, db); err != nil {
			db.Close()
			return nil, fmt.Errorf("run migrations: %w", err)
		}
		logger.Info("database migrations applied")
	}

	return db, nil
}

const (
	// authRateLimitRequests and authRateLimitWindow bound
	// authRateLimiter (see newServer): at most this many requests to
	// POST /auth/register or POST /auth/login combined, per client IP,
	// within this window. Chosen to be generous enough that a legitimate
	// user retrying a mistyped password a few times never notices it,
	// while still meaningfully slowing down credential stuffing or
	// registration spam against a single instance.
	authRateLimitRequests = 20
	authRateLimitWindow   = 5 * time.Minute

	// sessionCleanupInterval is how often expired sessions and stale
	// rate-limit windows are purged from memory — see
	// runPeriodicCleanup. Both are already handled correctly on the
	// per-request path regardless (ValidateToken rejects an expired
	// session; the rate limiter's own window rollover ignores a stale
	// counter) — this exists purely to bound memory/storage growth from
	// entries nobody ever comes back to touch again.
	sessionCleanupInterval = 1 * time.Hour
)

// runPeriodicCleanup periodically prunes expired sessions (via userSvc)
// and stale rate-limiter windows (via limiter) until ctx is canceled. A
// failed pass is logged but never treated as fatal — both underlying
// mechanisms remain correct on the per-request path even if a cleanup
// pass is skipped or fails, so a transient DB hiccup here must not crash
// the server or block request handling.
func runPeriodicCleanup(ctx context.Context, userSvc *user.Service, limiter *middleware.RateLimiter, logger *slog.Logger) {
	ticker := time.NewTicker(sessionCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := userSvc.PruneExpiredSessions(ctx); err != nil {
				logger.Error("session cleanup failed", "error", err)
			}
			limiter.Prune(time.Now())
		}
	}
}
