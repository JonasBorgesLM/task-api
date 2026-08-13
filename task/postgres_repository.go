package task

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// postgreSQLUniqueViolation is the PostgreSQL error code for a unique
// constraint violation (23505 — class 23 "integrity constraint
// violation"). See https://www.postgresql.org/docs/current/errcodes-appendix.html.
// It is hardcoded rather than imported from a constants package (e.g.
// github.com/jackc/pgerrcode) because Create is the only place that needs
// it — a whole dependency for one well-known, stable string is not
// justified.
const postgreSQLUniqueViolation = "23505"

// postgresRepository is a PostgreSQL implementation of Repository, backed
// by a *sql.DB (see NewPostgresRepository). It is unexported for the same
// reason memoryRepository is: callers outside this package depend only on
// the Repository interface, never on the concrete type.
//
// Every method takes the query's context.Context all the way down to the
// *sql.DB call (QueryRowContext/QueryContext/ExecContext/BeginTx), so a
// canceled request or an expired deadline aborts the in-flight query
// instead of running it to completion for no one.
//
// All queries are parameterized ($1, $2, ...); no user-supplied value is
// ever interpolated into SQL text, which is what rules out SQL injection
// here — see each method below for the exact query.
//
// postgresRepository holds no business logic: it does not decide which
// status transitions are legal, what "valid" input looks like, or how
// results should be ordered for a client. That all remains in Service.
// Where this file does make a query-shaping choice (e.g. FindAll's
// ORDER BY, described below), it is strictly a performance concern, never
// a behavioral one — Service's own ordering/validation is unaffected
// either way.
type postgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository returns a new PostgreSQL-backed Repository using
// db for all queries. db's connection pool (SetMaxOpenConns,
// SetMaxIdleConns, SetConnMaxLifetime) and lifecycle (Close) are the
// caller's responsibility — see cmd/api/main.go, the only place that
// opens or closes it. NewPostgresRepository does not ping db or run
// migrations; callers that need either must do so explicitly (see
// RunMigrations) before serving traffic.
func NewPostgresRepository(db *sql.DB) Repository {
	return &postgresRepository{db: db}
}

// Ping verifies that the underlying PostgreSQL connection pool can reach
// the database right now, satisfying the Pinger interface. It is used by
// cmd/api/health.go's readiness endpoint, not by any request path here —
// postgresRepository's other methods rely on *sql.DB's own connection
// handling and surface a failure to reach the database as a normal query
// error instead.
func (r *postgresRepository) Ping(ctx context.Context) error {
	return r.db.PingContext(ctx)
}

// Create persists a new task. Returns ErrAlreadyExists if the ID is
// already taken. The stored Version always starts at 1, regardless of
// what the caller set on task.Version — matching memoryRepository's
// contract.
//
// The task's ID is cast to uuid on the Postgres side ($1::uuid) rather
// than relying on implicit conversion, so a malformed ID is rejected by
// the database with a clear error instead of silently mismatching the
// column's type at the wire level.
func (r *postgresRepository) Create(ctx context.Context, task Task) error {
	const query = `
		INSERT INTO tasks (id, title, description, status, created_at, updated_at, version)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, 1)
	`

	_, err := r.db.ExecContext(ctx, query,
		task.ID, task.Title, task.Description, string(task.Status), task.CreatedAt, task.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("postgres: create task: %w", err)
	}

	return nil
}

// FindByID returns the task with the given ID. Returns ErrNotFound if
// absent.
func (r *postgresRepository) FindByID(ctx context.Context, id string) (Task, error) {
	const query = `
		SELECT id::text, title, description, status, created_at, updated_at, version
		FROM tasks
		WHERE id = $1::uuid
	`

	task, err := scanTask(r.db.QueryRowContext(ctx, query, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, ErrNotFound
		}
		return Task{}, fmt.Errorf("postgres: find task by id: %w", err)
	}

	return task, nil
}

// FindAll returns tasks ordered by (created_at, id) — per Repository's
// contract — windowed to at most limit results starting at offset.
//
// The window is applied in the query itself (LIMIT/OFFSET), not by
// fetching every row and slicing in Go: for a table with far more rows
// than any single page, that's the difference between a query that reads
// (roughly) offset+limit rows and one that reads and transfers the entire
// table on every call. limit < 0 ("no limit") is passed as SQL NULL, which
// PostgreSQL's LIMIT clause treats as "no limit" — i.e. LIMIT NULL is
// equivalent to omitting LIMIT entirely. The ORDER BY lets this use
// idx_tasks_created_at_id instead of an in-database sort as the table
// grows.
func (r *postgresRepository) FindAll(ctx context.Context, limit, offset int) ([]Task, error) {
	const query = `
		SELECT id::text, title, description, status, created_at, updated_at, version
		FROM tasks
		ORDER BY created_at, id
		LIMIT $1::bigint OFFSET $2::bigint
	`

	// limit < 0 ("no limit") must reach the query as SQL NULL, not as a
	// negative number — PostgreSQL's LIMIT rejects a negative value.
	var limitArg any
	if limit >= 0 {
		limitArg = limit
	}

	rows, err := r.db.QueryContext(ctx, query, limitArg, offset)
	if err != nil {
		return nil, fmt.Errorf("postgres: find all tasks: %w", err)
	}
	defer rows.Close()

	tasks := make([]Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scan task row: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: find all tasks: %w", err)
	}

	return tasks, nil
}

// Update replaces an existing task. Returns ErrNotFound if the ID does
// not exist, and ErrConflict if task.Version does not match the stored
// Version — i.e. the task was modified by another writer since task was
// read. On success the stored Version is incremented.
//
// The read-check-write sequence (read the current version, compare, then
// write) runs inside a single transaction with `SELECT ... FOR UPDATE`,
// which takes a row-level lock on the target task for the transaction's
// duration. That closes the race an unlocked read would leave open: two
// concurrent Updates could otherwise both read the same Version, both
// pass the comparison, and the second write would silently clobber the
// first. Locking the row makes the second transaction's SELECT block
// until the first commits (or rolls back), so it observes the
// already-incremented Version and correctly returns ErrConflict instead.
func (r *postgresRepository) Update(ctx context.Context, task Task) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres: update task: begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has succeeded

	var currentVersion int
	err = tx.QueryRowContext(ctx, `SELECT version FROM tasks WHERE id = $1::uuid FOR UPDATE`, task.ID).
		Scan(&currentVersion)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("postgres: update task: read current version: %w", err)
	}
	if currentVersion != task.Version {
		return ErrConflict
	}

	const query = `
		UPDATE tasks
		SET title = $1, description = $2, status = $3, updated_at = $4, version = version + 1
		WHERE id = $5::uuid
	`
	if _, err := tx.ExecContext(ctx, query,
		task.Title, task.Description, string(task.Status), task.UpdatedAt, task.ID,
	); err != nil {
		return fmt.Errorf("postgres: update task: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres: update task: commit transaction: %w", err)
	}

	return nil
}

// Delete removes the task with the given ID. Returns ErrNotFound if
// absent.
func (r *postgresRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("postgres: delete task: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: delete task: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}

	return nil
}

// taskScanner is satisfied by both *sql.Row and *sql.Rows, letting
// scanTask serve FindByID (a single row) and FindAll (many rows) without
// duplicating the column list and scan order between them.
type taskScanner interface {
	Scan(dest ...any) error
}

// scanTask reads one row in the fixed column order every query above
// selects: id, title, description, status, created_at, updated_at,
// version.
func scanTask(row taskScanner) (Task, error) {
	var (
		task   Task
		status string
	)

	if err := row.Scan(
		&task.ID, &task.Title, &task.Description, &status, &task.CreatedAt, &task.UpdatedAt, &task.Version,
	); err != nil {
		return Task{}, err
	}

	task.Status = Status(status)
	return task, nil
}

// isUniqueViolation reports whether err is a PostgreSQL unique constraint
// violation (SQLSTATE 23505) — the error Create observes when it races
// another writer inserting the same ID.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == postgreSQLUniqueViolation
}
