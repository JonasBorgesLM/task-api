// Command migrate applies or reverts PostgreSQL migrations against
// DATABASE_URL, independently of the API server. It exists for two cases
// the server's own automatic migration (cmd/api/main.go's buildRepository,
// controlled by DB_AUTO_MIGRATE) doesn't cover:
//
//   - Reverting a migration (migrate.RunMigrationsDown) — the server only
//     ever applies migrations forward.
//   - Deployments that set DB_AUTO_MIGRATE=false (e.g. multiple API
//     replicas, where every instance auto-migrating on boot is
//     undesirable) and instead want migrations run once, explicitly, as
//     their own deploy step.
//
// Usage:
//
//	DATABASE_URL=postgres://... go run ./cmd/migrate                # apply pending migrations
//	DATABASE_URL=postgres://... go run ./cmd/migrate -direction=down  # revert the last migration
//
// See also: `make migrate-up`, `make migrate-down`.
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

	// Registers the "pgx" driver with database/sql, the same driver
	// cmd/api/main.go uses — see its blank import for why this is the
	// only PostgreSQL-aware file besides that one.
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	direction := flag.String("direction", "up", `migration direction: "up" (apply pending migrations) or "down" (revert the last applied migration)`)
	flag.Parse()

	if err := run(*direction); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run(direction string) error {
	if direction != "up" && direction != "down" {
		return fmt.Errorf(`invalid -direction %q: want "up" or "down"`, direction)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.DatabaseURL == "" {
		return errors.New("DATABASE_URL must be set")
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

	switch direction {
	case "up":
		if err := migrate.RunMigrations(ctx, db); err != nil {
			return fmt.Errorf("apply migrations: %w", err)
		}
		fmt.Println("migrations applied")

	case "down":
		if err := migrate.RunMigrationsDown(ctx, db); err != nil {
			if errors.Is(err, migrate.ErrNoMigrationsToRevert) {
				fmt.Println("no migrations to revert")
				return nil
			}
			return fmt.Errorf("revert migration: %w", err)
		}
		fmt.Println("last migration reverted")
	}

	return nil
}
