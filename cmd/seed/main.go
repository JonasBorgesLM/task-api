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
// See also: `make seed`, `make seed-reset`, `make db-reset`.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
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

func main() {
	users := flag.Int("users", 5, "number of demo users to ensure exist")
	password := flag.String("password", "password123", "shared password for demo users (demo only — never reuse)")
	tasksPerUser := flag.Int("tasks-per-user", 10, "number of random tasks to create per user")
	reset := flag.Bool("reset", false, "delete every existing user, session and task before seeding")
	flag.Parse()

	if err := run(*users, *password, *tasksPerUser, *reset); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run(userCount int, password string, tasksPerUser int, reset bool) error {
	if userCount < 0 {
		return fmt.Errorf("invalid -users %d: must not be negative", userCount)
	}
	if tasksPerUser < 0 {
		return fmt.Errorf("invalid -tasks-per-user %d: must not be negative", tasksPerUser)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.DatabaseURL == "" {
		return errors.New("DATABASE_URL must be set — seeding the in-memory store has no observable effect")
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

	if reset {
		// sessions and tasks both reference users via a foreign key;
		// CASCADE lets a single statement empty all three regardless of
		// listing order. This is `make db-reset`'s entire implementation
		// (`seed -reset -users=0`) as well as `seed-reset`'s first step.
		if _, err := db.ExecContext(ctx, `TRUNCATE TABLE sessions, tasks, users CASCADE`); err != nil {
			return fmt.Errorf("reset database: %w", err)
		}
		fmt.Println("users, sessions and tasks tables emptied")
	}

	if userCount == 0 {
		fmt.Println("no users requested (-users=0); nothing seeded")
		return nil
	}

	userSvc := user.NewService(user.NewPostgresRepository(db), cfg.AuthSessionTTL)
	taskSvc := task.NewService(task.NewPostgresRepository(db))

	tasksCreated, statusCounts := 0, map[task.Status]int{}

	for i := 1; i <= userCount; i++ {
		email := fmt.Sprintf("demo-user-%d@example.com", i)

		u, err := userSvc.Register(ctx, email, password)
		if err != nil {
			if !errors.Is(err, user.ErrAlreadyExists) {
				return fmt.Errorf("register %s: %w", email, err)
			}
			// Already seeded by a prior run — reuse that account instead of
			// failing, so `make seed` is safe to run again to add more
			// tasks. Authenticate (rather than a direct lookup, which
			// Service doesn't expose) also confirms -password still
			// matches what that account was created with.
			u, err = userSvc.Authenticate(ctx, email, password)
			if err != nil {
				return fmt.Errorf("%s already exists with a different password than -password=%q "+
					"(run with -reset to start over): %w", email, password, err)
			}
		}

		for j := range tasksPerUser {
			title, description := randomTask()

			created, err := taskSvc.CreateTask(ctx, u.ID, title, description, string(randomPriority()))
			if err != nil {
				return fmt.Errorf("create task %d/%d for %s: %w", j+1, tasksPerUser, email, err)
			}
			tasksCreated++

			target := randomStatus()
			if target != task.StatusPending {
				if _, err := taskSvc.TransitionStatus(ctx, u.ID, created.ID, target); err != nil {
					return fmt.Errorf("transition task %d/%d for %s to %s: %w", j+1, tasksPerUser, email, target, err)
				}
			}
			statusCounts[target]++
		}
	}

	fmt.Printf("seeded %d demo users (password: %q) and %d tasks (pending=%d in_progress=%d done=%d cancelled=%d)\n",
		userCount, password, tasksCreated,
		statusCounts[task.StatusPending], statusCounts[task.StatusInProgress],
		statusCounts[task.StatusDone], statusCounts[task.StatusCancelled],
	)
	return nil
}
