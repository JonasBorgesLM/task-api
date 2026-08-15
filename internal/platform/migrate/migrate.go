// Package migrate applies and reverts the application's PostgreSQL schema
// migrations. It is a platform concern, not owned by any single domain
// package — internal/task and internal/user share one schema_migrations
// bookkeeping table and one embedded set of migration files, applied by
// this package's RunMigrations before either domain's postgresRepository
// serves a query.
package migrate

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// ErrNoMigrationsToRevert is returned by RunMigrationsDown when
// schema_migrations records no applied migration to roll back.
var ErrNoMigrationsToRevert = errors.New("no migrations to revert")

// migrationFiles embeds every migration under this package's migrations/ —
// both
// *.up.sql (applied by RunMigrations, automatically on every process
// startup when DB_AUTO_MIGRATE is enabled) and *.down.sql (applied by
// RunMigrationsDown, only ever run explicitly via `make migrate-down` /
// cmd/migrate) — so the compiled binary carries its own schema and needs
// no separate migration files deployed alongside it.
//
//go:embed migrations/*.sql
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

// RunMigrationsDown reverts the single most recently applied migration: it
// runs that migration's .down.sql and removes its schema_migrations
// record, both inside one transaction. It returns ErrNoMigrationsToRevert
// if schema_migrations is empty.
//
// This is the counterpart to RunMigrations used for manual rollback (see
// cmd/migrate and `make migrate-down`). The application itself never
// calls it — only RunMigrations runs automatically, from
// cmd/api/main.go's openDatabase.
func RunMigrationsDown(ctx context.Context, db *sql.DB) error {
	if err := ensureMigrationsTable(ctx, db); err != nil {
		return fmt.Errorf("postgres: ensure schema_migrations table: %w", err)
	}

	version, err := latestAppliedMigration(ctx, db)
	if err != nil {
		return fmt.Errorf("postgres: find latest applied migration: %w", err)
	}
	if version == "" {
		return ErrNoMigrationsToRevert
	}

	stmt, err := migrationFiles.ReadFile("migrations/" + version + ".down.sql")
	if err != nil {
		return fmt.Errorf("postgres: read down migration for %s: %w", version, err)
	}

	if err := revertMigration(ctx, db, version, string(stmt)); err != nil {
		return fmt.Errorf("postgres: revert migration %s: %w", version, err)
	}

	return nil
}

// latestAppliedMigration returns the version of the most recently applied
// migration (by applied_at), or "" if none has been applied yet.
func latestAppliedMigration(ctx context.Context, db *sql.DB) (string, error) {
	var version string
	err := db.QueryRowContext(ctx,
		`SELECT version FROM schema_migrations ORDER BY applied_at DESC LIMIT 1`,
	).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return version, err
}

// revertMigration runs stmt (a migration's .down.sql) and removes its
// schema_migrations record, atomically — the same reasoning as
// applyMigration, in reverse: a crash between the two steps must never
// leave the schema reverted but still recorded as applied, nor the record
// removed without the DDL having actually run.
func revertMigration(ctx context.Context, db *sql.DB, version, stmt string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has succeeded

	if _, err := tx.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("execute down migration DDL: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = $1`, version); err != nil {
		return fmt.Errorf("remove migration record: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
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
