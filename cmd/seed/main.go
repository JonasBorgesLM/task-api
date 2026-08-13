// Command seed populates the tasks table with randomly generated tasks,
// for local development and manual testing against a non-trivial dataset.
// It exists for the same reason cmd/migrate does: an operational concern
// unrelated to serving traffic, kept out of cmd/api/main.go entirely.
//
// Every task is created through the real task.Service (CreateTask, and
// CompleteTask for the fraction marked done) — the same validation, ID
// generation, and timestamp logic every other write path uses. Seeding
// never bypasses Service by writing to Repository directly.
//
// It requires DATABASE_URL: seeding the in-memory store would vanish the
// moment this process exits, with nothing left to have observed it.
//
// Usage:
//
//	DATABASE_URL=postgres://... go run ./cmd/seed                    # 20 random tasks
//	DATABASE_URL=postgres://... go run ./cmd/seed -count=100          # 100 random tasks
//	DATABASE_URL=postgres://... go run ./cmd/seed -done-ratio=0.5     # ~50% marked done
//	DATABASE_URL=postgres://... go run ./cmd/seed -reset              # empty the table first
//
// See also: `make seed` / `make seed-reset`.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"time"

	"github.com/JonasBorgesLM/task-api/config"
	"github.com/JonasBorgesLM/task-api/task"

	// Registers the "pgx" driver with database/sql, the same driver
	// cmd/api/main.go and cmd/migrate/main.go use.
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	count := flag.Int("count", 20, "number of random tasks to create")
	doneRatio := flag.Float64("done-ratio", 0.3, "fraction (0-1) of created tasks to mark as done")
	reset := flag.Bool("reset", false, "delete every existing task before seeding")
	flag.Parse()

	if err := run(*count, *doneRatio, *reset); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run(count int, doneRatio float64, reset bool) error {
	if count <= 0 {
		return fmt.Errorf("invalid -count %d: must be positive", count)
	}
	if doneRatio < 0 || doneRatio > 1 {
		return fmt.Errorf("invalid -done-ratio %v: must be between 0 and 1", doneRatio)
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
	if err := task.RunMigrations(ctx, db); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	if reset {
		if _, err := db.ExecContext(ctx, `TRUNCATE TABLE tasks`); err != nil {
			return fmt.Errorf("reset tasks table: %w", err)
		}
		fmt.Println("tasks table emptied")
	}

	svc := task.NewService(task.NewPostgresRepository(db))

	done := 0
	for i := range count {
		title, description := randomTask()

		created, err := svc.CreateTask(ctx, title, description)
		if err != nil {
			return fmt.Errorf("create task %d/%d: %w", i+1, count, err)
		}

		if rand.Float64() < doneRatio {
			if _, err := svc.CompleteTask(ctx, created.ID); err != nil {
				return fmt.Errorf("complete task %d/%d: %w", i+1, count, err)
			}
			done++
		}
	}

	fmt.Printf("seeded %d tasks (%d marked done)\n", count, done)
	return nil
}
