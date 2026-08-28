package attachment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// postgreSQLUniqueViolation and postgreSQLForeignKeyViolation are the
// PostgreSQL error codes for the two integrity constraints Create can
// trip (class 23). They are hardcoded rather than pulled from a constants
// package for the same reason internal/task hardcodes the first one: a
// whole dependency for two well-known, stable strings is not justified.
// See https://www.postgresql.org/docs/current/errcodes-appendix.html.
const (
	postgreSQLUniqueViolation     = "23505"
	postgreSQLForeignKeyViolation = "23503"
)

// postgresRepository is a PostgreSQL implementation of Repository.
// Unexported for the same reason memoryRepository is: callers outside
// this package depend only on the Repository interface.
//
// Every query carries the caller's context down to the *sql.DB call, so a
// canceled request aborts its in-flight query rather than running to
// completion for nobody.
//
// # Ownership is a join, not a second query
//
// Each statement below reaches the owner through `JOIN tasks`, so the
// ownership rule and the lookup are one operation. Fetching the row and
// then checking it would be two, with a window between them and an order
// a future edit can get wrong; expressing it as a join means a query that
// forgets the check returns nothing rather than returning somebody else's
// data.
type postgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository returns a Repository backed by db.
//
// Unlike NewMemoryRepository it takes no TaskOwnershipFunc: the ownership
// check is in the SQL. See Repository's doc comment for why both
// implementations must still answer identically.
func NewPostgresRepository(db *sql.DB) Repository {
	return &postgresRepository{db: db}
}

// attachmentColumns is the select list every read below shares. id and
// storage_key are cast to text because they are UUID columns and the
// domain model carries them as strings; without the cast, database/sql
// hands back []byte for them.
const attachmentColumns = `
	a.id::text, a.task_id::text, a.original_filename,
	a.storage_key::text, a.content_type, a.size_bytes, a.created_at
`

func (r *postgresRepository) Create(ctx context.Context, attachment Attachment, userID string) error {
	// INSERT ... SELECT rather than INSERT ... VALUES: the SELECT
	// produces a row only when the task exists *and* belongs to userID,
	// so an attempt to hang an attachment off somebody else's task
	// inserts nothing instead of being caught by a separate check that
	// could be skipped or race.
	const query = `
		INSERT INTO attachments (id, task_id, original_filename, storage_key, content_type, size_bytes, created_at)
		SELECT $1::uuid, t.id, $3, $4::uuid, $5, $6, $7
		FROM tasks t
		WHERE t.id = $2::uuid AND t.user_id = $8::uuid
	`

	result, err := r.db.ExecContext(ctx, query,
		attachment.ID, attachment.TaskID, attachment.OriginalFilename,
		attachment.StorageKey, attachment.ContentType, attachment.SizeBytes,
		attachment.CreatedAt, userID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrAlreadyExists
		}
		// The foreign key can still fire even though the SELECT filters
		// on tasks: the task can be deleted between the two, inside the
		// same statement's snapshot. Reporting it as ErrTaskNotFound
		// matches what the caller would have seen a moment earlier.
		if isForeignKeyViolation(err) {
			return ErrTaskNotFound
		}
		return fmt.Errorf("postgres: create attachment: %w", err)
	}

	// Zero rows means the SELECT matched nothing: no such task, or not
	// this user's. Both are reported the same way, so a caller cannot
	// use the error to learn that a task ID exists.
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: create attachment: %w", err)
	}
	if affected == 0 {
		return ErrTaskNotFound
	}

	return nil
}

func (r *postgresRepository) FindByStorageKey(ctx context.Context, storageKey, userID string) (Attachment, error) {
	const query = `
		SELECT ` + attachmentColumns + `
		FROM attachments a
		JOIN tasks t ON t.id = a.task_id
		WHERE a.storage_key = $1::uuid AND t.user_id = $2::uuid
	`

	att, err := scanAttachment(r.db.QueryRowContext(ctx, query, storageKey, userID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Covers both "no such key" and "a key belonging to someone
			// else", deliberately indistinguishable.
			return Attachment{}, ErrNotFound
		}
		return Attachment{}, fmt.Errorf("postgres: find attachment by storage key: %w", err)
	}

	return att, nil
}

// TotalBytesForUser sums size_bytes across every attachment owned
// through the user's tasks, in one aggregate query — the same join
// shape FindByStorageKey and FindByTask use, without the trip through
// Go to add up what the query itself can.
func (r *postgresRepository) TotalBytesForUser(ctx context.Context, userID string) (int64, error) {
	const query = `
		SELECT COALESCE(SUM(a.size_bytes), 0)
		FROM attachments a
		JOIN tasks t ON t.id = a.task_id
		WHERE t.user_id = $1::uuid
	`

	var total int64
	if err := r.db.QueryRowContext(ctx, query, userID).Scan(&total); err != nil {
		return 0, fmt.Errorf("postgres: total bytes for user: %w", err)
	}

	return total, nil
}

func (r *postgresRepository) FindByTask(ctx context.Context, taskID, userID string) ([]Attachment, error) {
	// The ownership check is its own statement here, unlike in the two
	// methods above, because this one has to tell "your task, no
	// attachments" apart from "not your task" — and an empty result set
	// cannot carry that distinction.
	const ownsQuery = `SELECT 1 FROM tasks WHERE id = $1::uuid AND user_id = $2::uuid`

	var exists int
	switch err := r.db.QueryRowContext(ctx, ownsQuery, taskID, userID).Scan(&exists); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrTaskNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: check task ownership: %w", err)
	}

	const query = `
		SELECT ` + attachmentColumns + `
		FROM attachments a
		WHERE a.task_id = $1::uuid
		ORDER BY a.created_at, a.id
	`

	rows, err := r.db.QueryContext(ctx, query, taskID)
	if err != nil {
		return nil, fmt.Errorf("postgres: find attachments by task: %w", err)
	}
	defer rows.Close()

	attachments := make([]Attachment, 0)
	for rows.Next() {
		att, err := scanAttachment(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scan attachment row: %w", err)
		}
		attachments = append(attachments, att)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: find attachments by task: %w", err)
	}

	return attachments, nil
}

// rowScanner is what *sql.Row and *sql.Rows have in common, so one
// scanning function serves both the single-row and the multi-row reads.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanAttachment(row rowScanner) (Attachment, error) {
	var att Attachment
	err := row.Scan(
		&att.ID, &att.TaskID, &att.OriginalFilename,
		&att.StorageKey, &att.ContentType, &att.SizeBytes, &att.CreatedAt,
	)
	if err != nil {
		return Attachment{}, err
	}
	return att, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == postgreSQLUniqueViolation
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == postgreSQLForeignKeyViolation
}

func (r *postgresRepository) UnreferencedKeys(ctx context.Context, keys []string) ([]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	// One statement rather than a query per key: the collector runs over
	// everything in the store, and a round trip each would make the pass
	// cost scale with the number of files rather than with the number of
	// orphans.
	//
	// The candidate keys arrive as an array and the join finds which of
	// them have no row. They are cast to uuid because storage_key is a
	// uuid column — and that cast is also a filter: a name in the store
	// that is not a UUID cannot be a storage key this application wrote,
	// so it must not be reported as an unreferenced one for deletion.
	// Hence the WHERE on the input side, before the cast.
	const query = `
		SELECT k
		FROM unnest($1::text[]) AS k
		WHERE k ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
		  AND NOT EXISTS (
		      SELECT 1 FROM attachments a WHERE a.storage_key = k::uuid
		  )
	`

	rows, err := r.db.QueryContext(ctx, query, pqTextArray(keys))
	if err != nil {
		return nil, fmt.Errorf("postgres: find unreferenced storage keys: %w", err)
	}
	defer rows.Close()

	unreferenced := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("postgres: scan unreferenced key: %w", err)
		}
		unreferenced = append(unreferenced, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: find unreferenced storage keys: %w", err)
	}

	return unreferenced, nil
}

// pqTextArray renders keys as a PostgreSQL text[] literal.
//
// database/sql has no array type of its own, and pgx's stdlib driver
// passes a []string through as an opaque value rather than as an array,
// so the literal is built here. Each element is double-quoted with
// backslashes and quotes escaped — the keys are server-generated UUIDs
// and cannot contain either, but building the literal correctly is
// cheaper than relying on that and re-auditing it if key generation ever
// changes.
func pqTextArray(keys []string) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		for _, r := range key {
			if r == '"' || r == '\\' {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		}
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}
