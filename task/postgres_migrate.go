package task

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// migrationFiles embeds every "up" migration under task/migrations so the
// compiled binary carries its own schema and needs no separate migration
// files deployed alongside it. Only *.up.sql is embedded: the matching
// *.down.sql files exist on disk for manual rollback (psql, or any
// migrate-style CLI pointed at the directory) but are not needed at
// runtime, since RunMigrations only ever applies forward.
//
//go:embed migrations/*.up.sql
var migrationFiles embed.FS

// RunMigrations applies every embedded migration that schema_migrations
// does not yet record, in filename order, each inside its own transaction
// so a failure partway through a migration is rolled back rather than
// left half-applied. It is safe to call on every process startup —
// already-applied migrations are skipped — which is what cmd/api/main.go
// does when DB_AUTO_MIGRATE is enabled.
func RunMigrations(ctx context.Context, db *sql.DB) error {
	if err := ensureMigrationsTable(ctx, db); err != nil {
		return fmt.Errorf("postgres: ensure schema_migrations table: %w", err)
	}

	paths, err := fs.Glob(migrationFiles, "migrations/*.up.sql")
	if err != nil {
		return fmt.Errorf("postgres: list migrations: %w", err)
	}
	sort.Strings(paths)

	for _, p := range paths {
		version := strings.TrimSuffix(path.Base(p), ".up.sql")

		applied, err := migrationApplied(ctx, db, version)
		if err != nil {
			return fmt.Errorf("postgres: check migration %s: %w", version, err)
		}
		if applied {
			continue
		}

		stmt, err := migrationFiles.ReadFile(p)
		if err != nil {
			return fmt.Errorf("postgres: read migration %s: %w", version, err)
		}

		if err := applyMigration(ctx, db, version, string(stmt)); err != nil {
			return fmt.Errorf("postgres: apply migration %s: %w", version, err)
		}
	}

	return nil
}

// ensureMigrationsTable creates the bookkeeping table RunMigrations uses to
// track which migrations have already been applied, if it does not exist
// yet.
func ensureMigrationsTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	return err
}

// migrationApplied reports whether version is already recorded in
// schema_migrations.
func migrationApplied(ctx context.Context, db *sql.DB, version string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`,
		version,
	).Scan(&exists)
	return exists, err
}

// applyMigration runs stmt and records version as applied, atomically: both
// happen inside a single transaction, so a crash or error between the two
// can never leave the database with the migration's DDL applied but not
// recorded (which would cause it to be silently re-applied next startup),
// nor recorded without the DDL having actually run.
func applyMigration(ctx context.Context, db *sql.DB, version, stmt string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has succeeded

	if _, err := tx.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("execute migration DDL: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
		return fmt.Errorf("record migration version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}
