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

	"github.com/JonasBorgesLM/moat/ratelimit"
	"github.com/JonasBorgesLM/moat/secureheaders"

	"github.com/JonasBorgesLM/task-api/internal/config"
	"github.com/JonasBorgesLM/task-api/internal/middleware"
	"github.com/JonasBorgesLM/task-api/internal/platform/migrate"
	"github.com/JonasBorgesLM/task-api/internal/task"
	"github.com/JonasBorgesLM/task-api/internal/user"

	// Registers the "pgx" driver with database/sql under the name used by
	// sql.Open below. cmd/migrate and cmd/seed carry the same blank import
	// for the *sql.DB each of them opens, and the two postgresRepository
	// implementations import pgx/v5/pgconn to inspect SQLSTATE codes — a
	// PostgreSQL import is expected in all of those places.
	//
	// What stays confined is the *decision*: openDatabase and newServer
	// below are the only place that picks which Repository implementation
	// backs the process. Every Service and Handler — task's and user's
	// alike — knows nothing about PostgreSQL or database/sql, and depends
	// solely on its own package's Repository interface.
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

	// Three rate-limit tiers, each answering a threat the others cannot.
	// All are token buckets over a bounded in-process store: a fixed
	// window would let a client spend its whole quota at the end of one
	// window and again at the start of the next, doubling the intended
	// rate at every boundary.
	//
	// globalLimiter is keyed by RemoteAddr and wraps the entire mux, so it
	// bounds a single source across every route — including the session
	// lookup RequireAuth performs, which userLimiter by construction
	// cannot protect (see below).
	//
	// authLimiter is much tighter and covers only /auth/register and
	// /auth/login, where an unauthenticated caller can burn a bcrypt
	// comparison per request.
	//
	// userLimiter is keyed by authenticated user ID rather than address,
	// so one account cannot multiply its allowance by connecting from
	// many hosts. It necessarily runs *after* RequireAuth — the user ID
	// it keys on does not exist before then — which is precisely why
	// globalLimiter has to exist in front rather than be replaced by it.
	globalLimiter := ratelimit.New(cfg.RateLimitBurst, cfg.RateLimitPerSec)
	authLimiter := ratelimit.New(cfg.AuthRateLimitBurst, cfg.AuthRateLimitPerSec)
	userLimiter := ratelimit.New(
		cfg.UserRateLimitBurst,
		cfg.UserRateLimitPerSec,
		ratelimit.WithKeyFunc(userIDKey),
	)

	// authenticated is what every non-public route is wrapped in:
	// authenticate first, then charge the request to that user's bucket.
	// The nesting order is load-bearing — userLimiter's key function
	// reads the user ID that RequireAuth puts in the request context, so
	// running the limiter on the outside would leave it with no key at
	// all on every request.
	authenticated := func(next http.Handler) http.Handler {
		return requireAuth(userLimiter.Middleware(next))
	}

	mux := http.NewServeMux()

	// /debug/vars is authenticated, unlike the two health routes above.
	// expvar exposes the process's command line and full runtime/GC
	// statistics — internal operational detail with no reason to be
	// world-readable, and a free fingerprint of the deployment for anyone
	// probing it. The health routes stay public because an orchestrator's
	// probe has no credentials to offer; expvar has no such constraint,
	// since the humans and scrapers who read it can carry a token.
	mux.Handle("GET /debug/vars", authenticated(expvar.Handler()))

	userHandler.RegisterRoutes(mux, authenticated, authLimiter.Middleware)
	taskHandler.RegisterRoutes(mux, authenticated)

	// The liveness and readiness probes deliberately sit outside the
	// global rate limiter, on an outer mux whose catch-all sends
	// everything else through it. ServeMux prefers the more specific
	// pattern, so the two health routes below win over "/".
	//
	// A rate-limited health check is an availability bug waiting to
	// happen: an orchestrator reads 429 as "this replica is unhealthy"
	// and kills a process that was serving perfectly well, converting a
	// burst of client traffic into a restart loop. The probes are also
	// the one caller that cannot back off and retry on its own schedule.
	// They are cheap, they expose nothing (see registerReadinessRoute),
	// and their volume is set by the orchestrator rather than by a
	// client, so there is nothing here for a limiter to protect.
	root := http.NewServeMux()
	registerHealthRoute(root, logger)
	registerReadinessRoute(root, db, logger)
	root.Handle("/", globalLimiter.Middleware(mux))

	go runPeriodicCleanup(ctx, userSvc, logger)

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
	//
	// secureheaders sits outside CORS, and therefore outside everything
	// that can answer a request without reaching the mux: it has to run
	// before the preflight CORS short-circuits, before the 429 the rate
	// limiter writes, before the 500 Recovery writes for a panicking
	// handler, and before the router's own 404/405 — a response that
	// skips the handler is exactly the kind that would otherwise go out
	// bare.
	//
	// The global rate limiter is not a link in this chain: it is mounted
	// on the outer mux's catch-all (see root above) so the health probes
	// can bypass it. That placement also puts it inside CORS, which is
	// what we want anyway — a 429 without Access-Control-Allow-Origin is
	// one the browser refuses to expose to the calling script, surfacing
	// as an opaque network error instead of a status the client can act
	// on.
	rootHandler := middleware.Chain(
		middleware.RequestID,
		middleware.Logging(logger),
		secureheaders.Middleware(
			// This API only ever returns JSON, so nothing it serves
			// legitimately loads a script, stylesheet, image or font.
			// "default-src 'none'" denies every fetch directive at once
			// and is strictly tighter than the library's default of
			// 'self'. base-uri, form-action and frame-ancestors do not
			// inherit from default-src, so each is named explicitly.
			secureheaders.WithCSP("default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"),
			// includeSubdomains and preload stay off: this process knows
			// its own scheme, not what else is served under the parent
			// domain, and preload is close to irreversible. See
			// config.Config.HSTSMaxAge for why the header itself is sent
			// unconditionally.
			secureheaders.WithHSTS(cfg.HSTSMaxAge, false, false),
		),
		middleware.CORS(cfg.CORSAllowedOrigins),
		middleware.Recovery(logger),
	)(root)

	// HTTP server with explicit timeouts, sourced entirely from Config.
	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      rootHandler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	// Each Limiter owns the in-process store it created, and Close stops
	// the goroutine that expires idle buckets in it. Closing the database
	// alone would leave three of those running for the lifetime of the
	// process — harmless in main(), a leak across a test suite that
	// builds a server per test.
	closeAll := func() error {
		globalLimiter.Close()
		authLimiter.Close()
		userLimiter.Close()
		return closeDB()
	}

	return srv, closeAll, nil
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
	// sessionCleanupInterval is how often expired sessions are purged
	// from storage — see runPeriodicCleanup. Expiry is already enforced
	// on the per-request path regardless (ValidateToken rejects and
	// deletes an expired session on next use); this exists purely to
	// reclaim sessions nobody ever comes back to touch again.
	sessionCleanupInterval = 1 * time.Hour
)

// userIDKey keys the per-user rate limiter by the authenticated user's
// ID, which user.RequireAuth places in the request context.
//
// It returns ratelimit.ErrNoKey rather than falling back to the client
// address when no user is present. The fallback would look more forgiving
// and be strictly worse: this limiter is only ever mounted behind
// RequireAuth, so a request reaching it without a user ID means the
// wiring changed underneath it, and silently re-keying by address would
// hide that while quietly merging every such request into one shared
// bucket. ErrNoKey makes the limiter fail closed and surface the
// misconfiguration instead.
func userIDKey(r *http.Request) (string, error) {
	id, ok := middleware.UserIDFromContext(r.Context())
	if !ok || id == "" {
		return "", ratelimit.ErrNoKey
	}
	return id, nil
}

// runPeriodicCleanup periodically prunes expired sessions until ctx is
// canceled. A failed pass is logged but never treated as fatal — session
// expiry is enforced on the per-request path regardless (ValidateToken
// rejects and deletes an expired session on next use), so a transient DB
// hiccup here must not crash the server or block request handling.
//
// The rate limiters used to be swept here too. They no longer need it:
// each owns a bounded store that caps how many buckets it tracks and
// expires idle ones on its own, which is a stronger guarantee than a
// periodic sweep of an unbounded map — a sweep every hour still leaves
// 59 minutes for a client cycling through an IPv6 /64 to grow that map
// without limit.
func runPeriodicCleanup(ctx context.Context, userSvc *user.Service, logger *slog.Logger) {
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
		}
	}
}
