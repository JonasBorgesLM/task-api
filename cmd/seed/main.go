// Command seed populates the database with demo users and randomly
// generated tasks for local development and manual testing against a
// non-trivial, multi-user dataset. It exists for the same reason
// cmd/migrate does: an operational concern unrelated to serving traffic,
// kept out of cmd/api/main.go entirely.
//
// Every user is created through the real user.Service (Register) and
// every task through the real task.Service (CreateTask, and
// TransitionStatus for the fraction moved off pending) — the same
// validation, ID generation, and timestamp logic every other write path
// uses. Seeding never bypasses either Service by writing to a Repository
// directly.
//
// It requires DATABASE_URL: seeding the in-memory store would vanish the
// moment this process exits, with nothing left to have observed it.
//
// Usage:
//
//	DATABASE_URL=postgres://... go run ./cmd/seed                          # 5 users, 10 tasks each
//	DATABASE_URL=postgres://... go run ./cmd/seed -users=20 -tasks-per-user=50
//	DATABASE_URL=postgres://... go run ./cmd/seed -reset                    # wipe users/sessions/tasks, then seed
//	DATABASE_URL=postgres://... go run ./cmd/seed -reset -users=0           # wipe only, seed nothing
//
// -reset is irreversible: it TRUNCATEs users, sessions and tasks. It is
// refused outright unless DATABASE_URL points at a local database (see
// isLocalDatabase), so a copy-pasted production connection string fails
// safely instead of emptying it; -allow-remote-reset overrides that when
// the target really is intended.
//
// See also: `make seed`, `make seed-reset`, `make db-reset`.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/JonasBorgesLM/task-api/internal/config"
	"github.com/JonasBorgesLM/task-api/internal/platform/migrate"
	"github.com/JonasBorgesLM/task-api/internal/task"
	"github.com/JonasBorgesLM/task-api/internal/user"

	// Registers the "pgx" driver with database/sql, the same driver
	// cmd/api/main.go and cmd/migrate/main.go use.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// seedOptions bundles one seeding run's parameters. It is a struct rather
// than five positional arguments because the last two are both bools:
// `run(2, "pw", 3, false, false)` gives a reader no way to tell at the
// call site which flag is which, and one of them guards an irreversible
// TRUNCATE.
type seedOptions struct {
	users            int
	password         string
	tasksPerUser     int
	reset            bool
	allowRemoteReset bool
}

func main() {
	users := flag.Int("users", 5, "number of demo users to ensure exist")
	password := flag.String("password", "password123", "shared password for demo users (demo only — never reuse)")
	tasksPerUser := flag.Int("tasks-per-user", 10, "number of random tasks to create per user")
	reset := flag.Bool("reset", false, "delete every existing user, session and task before seeding")
	allowRemoteReset := flag.Bool("allow-remote-reset", false,
		"permit -reset against a non-local DATABASE_URL (required safety acknowledgement; see docs)")
	flag.Parse()

	opts := seedOptions{
		users:            *users,
		password:         *password,
		tasksPerUser:     *tasksPerUser,
		reset:            *reset,
		allowRemoteReset: *allowRemoteReset,
	}

	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run(opts seedOptions) error {
	if opts.users < 0 {
		return fmt.Errorf("invalid -users %d: must not be negative", opts.users)
	}
	if opts.tasksPerUser < 0 {
		return fmt.Errorf("invalid -tasks-per-user %d: must not be negative", opts.tasksPerUser)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.DatabaseURL == "" {
		return errors.New("DATABASE_URL must be set — seeding the in-memory store has no observable effect")
	}

	// -reset TRUNCATEs every table, with no undo. Refuse to do that to a
	// database this command can't positively identify as local unless the
	// caller says so explicitly. `make seed-reset` / `make db-reset` point
	// at localhost and are unaffected; a mistyped or copy-pasted
	// production DATABASE_URL is caught here instead of after the fact.
	if opts.reset && !opts.allowRemoteReset && !isLocalDatabase(cfg.DatabaseURL) {
		return errors.New("refusing -reset against a non-local DATABASE_URL: " +
			"-reset irreversibly TRUNCATEs users, sessions and tasks. " +
			"Re-run with -allow-remote-reset if this really is the database you meant")
	}

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	// Applying migrations here (rather than requiring a separate
	// `make migrate-up` first) is what lets seeding "just work" against a
	// freshly created, empty database — RunMigrations is a no-op if
	// everything is already applied.
	if err := migrate.RunMigrations(ctx, db); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	if opts.reset {
		// sessions and tasks both reference users via a foreign key;
		// CASCADE lets a single statement empty all three regardless of
		// listing order. This is `make db-reset`'s entire implementation
		// (`seed -reset -users=0`) as well as `seed-reset`'s first step.
		if _, err := db.ExecContext(ctx, `TRUNCATE TABLE sessions, tasks, users CASCADE`); err != nil {
			return fmt.Errorf("reset database: %w", err)
		}
		fmt.Println("users, sessions and tasks tables emptied")
	}

	if opts.users == 0 {
		fmt.Println("no users requested (-users=0); nothing seeded")
		return nil
	}

	userSvc := user.NewService(user.NewPostgresRepository(db), cfg.AuthSessionTTL, cfg.AuthMaxSessionsPerUser)
	taskSvc := task.NewService(task.NewPostgresRepository(db))

	tasksCreated, statusCounts := 0, map[task.Status]int{}

	for i := 1; i <= opts.users; i++ {
		email := fmt.Sprintf("demo-user-%d@example.com", i)

		u, err := userSvc.Register(ctx, email, opts.password)
		if err != nil {
			if !errors.Is(err, user.ErrAlreadyExists) {
				return fmt.Errorf("register %s: %w", email, err)
			}
			// Already seeded by a prior run — reuse that account instead of
			// failing, so `make seed` is safe to run again to add more
			// tasks. Authenticate (rather than a direct lookup, which
			// Service doesn't expose) also confirms -password still
			// matches what that account was created with.
			u, err = userSvc.Authenticate(ctx, email, opts.password)
			if err != nil {
				return fmt.Errorf("%s already exists with a different password than -password=%q "+
					"(run with -reset to start over): %w", email, opts.password, err)
			}
		}

		for j := range opts.tasksPerUser {
			title, description := randomTask()

			created, err := taskSvc.CreateTask(ctx, u.ID, title, description, string(randomPriority()))
			if err != nil {
				return fmt.Errorf("create task %d/%d for %s: %w", j+1, opts.tasksPerUser, email, err)
			}
			tasksCreated++

			target := randomStatus()
			if target != task.StatusPending {
				if _, err := taskSvc.TransitionStatus(ctx, u.ID, created.ID, target); err != nil {
					return fmt.Errorf("transition task %d/%d for %s to %s: %w", j+1, opts.tasksPerUser, email, target, err)
				}
			}
			statusCounts[target]++
		}
	}

	fmt.Printf("seeded %d demo users (password: %q) and %d tasks (pending=%d in_progress=%d done=%d cancelled=%d)\n",
		opts.users, opts.password, tasksCreated,
		statusCounts[task.StatusPending], statusCounts[task.StatusInProgress],
		statusCounts[task.StatusDone], statusCounts[task.StatusCancelled],
	)
	return nil
}

// isLocalDatabase reports whether databaseURL demonstrably points at a
// database on this machine. It is used only to decide whether -reset needs
// an explicit acknowledgement, so it is deliberately biased toward
// answering false:
//
//   - Anything that isn't a postgres:// URL — notably libpq's
//     "host=... user=..." key/value DSN form — returns false, because the
//     target can't be read reliably without reimplementing libpq's parser.
//   - A URL with no host (a Unix socket) also returns false.
//
// A false negative costs the caller one extra flag; a false positive would
// TRUNCATE a database nobody meant to touch. Only the first of those is
// recoverable.
func isLocalDatabase(databaseURL string) bool {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return false
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return false
	}

	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}
