//go:build integration

package attachment

// Integration tests for postgresRepository, run against a real PostgreSQL
// instance. The "integration" build tag is what keeps these separate from
// the unit suite: a plain `go test ./...` never compiles this file, so it
// can neither run by accident nor be silently skipped and reported as
// passing. See internal/task/postgres_repository_test.go's header for the
// full rationale and the commands to run them.
//
// These matter more here than the memory tests do. The ownership rule
// this package enforces is expressed twice — as a TaskOwnershipFunc in
// memory, as a JOIN in SQL — and only this file can catch the two
// disagreeing.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/JonasBorgesLM/task-api/internal/platform/migrate"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const testDatabaseURLEnv = "TEST_DATABASE_URL"

// newUUID generates a random UUID v4. The columns these tests write are
// UUID-typed, so the short fixed IDs memory_repository_test.go uses would
// fail the ::uuid cast.
func newUUID(t *testing.T) string {
	t.Helper()

	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate uuid: %v", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// newPostgresTestRepo connects to TEST_DATABASE_URL, migrates, empties
// every table these tests touch, and returns a ready repository plus two
// users and a task belonging to the first. Two users because every
// interesting assertion here is about what the second one cannot reach.
func newPostgresTestRepo(t *testing.T) (repo Repository, db *sql.DB, owner, stranger, taskID string) {
	t.Helper()

	url := os.Getenv(testDatabaseURLEnv)
	if url == "" {
		t.Skipf("%s not set; skipping PostgreSQL integration test (see docker-compose.yml)", testDatabaseURLEnv)
	}

	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping database: %v", err)
	}
	if err := migrate.RunMigrations(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if _, err := db.ExecContext(ctx, `TRUNCATE TABLE attachments, sessions, tasks, users CASCADE`); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}

	owner = insertUser(t, db)
	stranger = insertUser(t, db)
	taskID = insertTask(t, db, owner)

	return NewPostgresRepository(db), db, owner, stranger, taskID
}

// insertUser and insertTask write directly via SQL: this file tests
// attachment's Repository, not task's or user's, so it does not import
// those packages merely to satisfy two foreign keys.
func insertUser(t *testing.T, db *sql.DB) string {
	t.Helper()

	id := newUUID(t)
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO users (id, email, password_hash) VALUES ($1::uuid, $2, 'not-a-real-hash')
	`, id, id+"@example.com")
	if err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	return id
}

func insertTask(t *testing.T, db *sql.DB, userID string) string {
	t.Helper()

	id := newUUID(t)
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO tasks (id, user_id, title, description, status, priority, created_at, updated_at, version)
		VALUES ($1::uuid, $2::uuid, 'a task', '', 'pending', 'medium', now(), now(), 1)
	`, id, userID)
	if err != nil {
		t.Fatalf("insert test task: %v", err)
	}
	return id
}

func newPostgresAttachment(t *testing.T, taskID string) Attachment {
	t.Helper()

	return Attachment{
		ID:               newUUID(t),
		TaskID:           taskID,
		OriginalFilename: "report.pdf",
		StorageKey:       newUUID(t),
		ContentType:      "application/pdf",
		SizeBytes:        2048,
		// Truncated to microseconds because TIMESTAMPTZ stores no finer,
		// so an untruncated value would not survive the round trip.
		CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
	}
}

func TestPostgres_CreateAndFindByStorageKey(t *testing.T) {
	repo, _, owner, _, taskID := newPostgresTestRepo(t)
	att := newPostgresAttachment(t, taskID)

	if err := repo.Create(context.Background(), att, owner); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	got, err := repo.FindByStorageKey(context.Background(), att.StorageKey, owner)
	if err != nil {
		t.Fatalf("FindByStorageKey() unexpected error: %v", err)
	}

	if got.ID != att.ID || got.TaskID != att.TaskID ||
		got.OriginalFilename != att.OriginalFilename || got.StorageKey != att.StorageKey ||
		got.ContentType != att.ContentType || got.SizeBytes != att.SizeBytes {
		t.Errorf("FindByStorageKey() = %+v, want %+v", got, att)
	}
	if !got.CreatedAt.Equal(att.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, att.CreatedAt)
	}
}

// TestPostgres_Create_OnSomeoneElsesTask is the SQL half of the rule the
// memory implementation expresses as a function call. The INSERT ...
// SELECT must match no rows and therefore write nothing.
func TestPostgres_Create_OnSomeoneElsesTask(t *testing.T) {
	repo, db, _, stranger, taskID := newPostgresTestRepo(t)

	err := repo.Create(context.Background(), newPostgresAttachment(t, taskID), stranger)
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("Create() error = %v, want ErrTaskNotFound", err)
	}

	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM attachments`).Scan(&count); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if count != 0 {
		t.Errorf("attachments table has %d rows after a refused Create, want 0", count)
	}
}

func TestPostgres_Create_OnMissingTask(t *testing.T) {
	repo, _, owner, _, _ := newPostgresTestRepo(t)

	err := repo.Create(context.Background(), newPostgresAttachment(t, newUUID(t)), owner)
	if !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("Create() error = %v, want ErrTaskNotFound", err)
	}
}

func TestPostgres_Create_DuplicateStorageKey(t *testing.T) {
	repo, _, owner, _, taskID := newPostgresTestRepo(t)

	first := newPostgresAttachment(t, taskID)
	if err := repo.Create(context.Background(), first, owner); err != nil {
		t.Fatalf("first Create() unexpected error: %v", err)
	}

	second := newPostgresAttachment(t, taskID)
	second.StorageKey = first.StorageKey

	if err := repo.Create(context.Background(), second, owner); !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("Create() error = %v, want ErrAlreadyExists", err)
	}
}

func TestPostgres_FindByStorageKey_KeyIsNotACapability(t *testing.T) {
	repo, _, owner, stranger, taskID := newPostgresTestRepo(t)
	att := newPostgresAttachment(t, taskID)

	if err := repo.Create(context.Background(), att, owner); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if _, err := repo.FindByStorageKey(context.Background(), att.StorageKey, stranger); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByStorageKey() with a valid key from a non-owner = %v, want ErrNotFound", err)
	}
}

func TestPostgres_FindByTask_OrdersAndScopes(t *testing.T) {
	repo, _, owner, stranger, taskID := newPostgresTestRepo(t)

	base := time.Now().UTC().Truncate(time.Microsecond)
	for i, offset := range []time.Duration{2 * time.Second, 0, time.Second} {
		att := newPostgresAttachment(t, taskID)
		att.CreatedAt = base.Add(offset)
		att.OriginalFilename = fmt.Sprintf("file-%d.pdf", i)
		if err := repo.Create(context.Background(), att, owner); err != nil {
			t.Fatalf("Create(%d) unexpected error: %v", i, err)
		}
	}

	got, err := repo.FindByTask(context.Background(), taskID, owner)
	if err != nil {
		t.Fatalf("FindByTask() unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("FindByTask() returned %d attachments, want 3", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].CreatedAt.Before(got[i-1].CreatedAt) {
			t.Errorf("FindByTask() is not ordered by created_at: %v before %v", got[i-1].CreatedAt, got[i].CreatedAt)
		}
	}

	if _, err := repo.FindByTask(context.Background(), taskID, stranger); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("FindByTask() for a stranger = %v, want ErrTaskNotFound", err)
	}
}

func TestPostgres_FindByTask_OwnerWithNoAttachments(t *testing.T) {
	repo, _, owner, _, taskID := newPostgresTestRepo(t)

	got, err := repo.FindByTask(context.Background(), taskID, owner)
	if err != nil {
		t.Fatalf("FindByTask() unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("FindByTask() = %v, want empty", got)
	}
	if got == nil {
		t.Error("FindByTask() returned nil, want an empty non-nil slice")
	}
}

// TestPostgres_Schema_DeletingTaskCascadesRows documents what the ON
// DELETE CASCADE does and — just as importantly — what it does not. The
// rows go; the blobs those rows pointed at are not this schema's to
// reach, and nothing here removes them.
func TestPostgres_Schema_DeletingTaskCascadesRows(t *testing.T) {
	repo, db, owner, _, taskID := newPostgresTestRepo(t)

	att := newPostgresAttachment(t, taskID)
	if err := repo.Create(context.Background(), att, owner); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if _, err := db.ExecContext(context.Background(), `DELETE FROM tasks WHERE id = $1::uuid`, taskID); err != nil {
		t.Fatalf("delete task: %v", err)
	}

	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM attachments`).Scan(&count); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if count != 0 {
		t.Errorf("attachments table has %d rows after its task was deleted, want 0", count)
	}
}

// TestPostgres_Schema_RejectsNegativeSize covers the CHECK constraint. A
// negative size is not a small file, it is a bug, and the column exists
// to stop that being persisted silently.
func TestPostgres_Schema_RejectsNegativeSize(t *testing.T) {
	_, db, _, _, taskID := newPostgresTestRepo(t)

	_, err := db.ExecContext(context.Background(), `
		INSERT INTO attachments (id, task_id, original_filename, storage_key, content_type, size_bytes)
		VALUES ($1::uuid, $2::uuid, 'x', $3::uuid, 'text/plain', -1)
	`, newUUID(t), taskID, newUUID(t))
	if err == nil {
		t.Error("INSERT with a negative size_bytes succeeded, want the CHECK constraint to reject it")
	}
}

// TestPostgres_UnreferencedKeys covers the query the orphan collector
// depends on, including the two ways it must answer "no" — a key that a
// row references, and a name that could not be a storage key at all.
func TestPostgres_UnreferencedKeys(t *testing.T) {
	repo, _, owner, _, taskID := newPostgresTestRepo(t)

	referenced := newPostgresAttachment(t, taskID)
	if err := repo.Create(context.Background(), referenced, owner); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	orphan := newUUID(t)

	got, err := repo.UnreferencedKeys(context.Background(), []string{
		referenced.StorageKey, // has a row
		orphan,                // does not
		"README",              // not a storage key at all
		"../../etc/passwd",    // nor this
	})
	if err != nil {
		t.Fatalf("UnreferencedKeys() unexpected error: %v", err)
	}

	if len(got) != 1 || got[0] != orphan {
		t.Errorf("UnreferencedKeys() = %v, want exactly [%s]", got, orphan)
	}
}

// TestPostgres_UnreferencedKeys_AfterTaskDeleteCascade is the scenario
// the collector was written for, end to end at the storage layer:
// deleting a task cascades the attachment row away, and the storage key
// it pointed at becomes collectable.
func TestPostgres_UnreferencedKeys_AfterTaskDeleteCascade(t *testing.T) {
	repo, db, owner, _, taskID := newPostgresTestRepo(t)

	att := newPostgresAttachment(t, taskID)
	if err := repo.Create(context.Background(), att, owner); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	// Before the delete it is referenced and must not be collectable.
	before, err := repo.UnreferencedKeys(context.Background(), []string{att.StorageKey})
	if err != nil {
		t.Fatalf("UnreferencedKeys() unexpected error: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("UnreferencedKeys() = %v before the task was deleted, want empty", before)
	}

	if _, err := db.ExecContext(context.Background(), `DELETE FROM tasks WHERE id = $1::uuid`, taskID); err != nil {
		t.Fatalf("delete task: %v", err)
	}

	after, err := repo.UnreferencedKeys(context.Background(), []string{att.StorageKey})
	if err != nil {
		t.Fatalf("UnreferencedKeys() unexpected error: %v", err)
	}
	if len(after) != 1 || after[0] != att.StorageKey {
		t.Errorf("UnreferencedKeys() = %v after the cascade, want [%s]", after, att.StorageKey)
	}
}

// TestPostgres_UnreferencedKeys_ScopelessByDesign pins the one place
// this package deliberately ignores ownership. Scoping the query to a
// user would report another user's live attachment as unreferenced, and
// the collector would then delete it.
func TestPostgres_UnreferencedKeys_ScopelessByDesign(t *testing.T) {
	repo, db, owner, stranger, taskID := newPostgresTestRepo(t)

	// An attachment on a second task, owned by the other user.
	strangerTask := newUUID(t)
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO tasks (id, user_id, title, description, status, priority, created_at, updated_at, version)
		VALUES ($1::uuid, $2::uuid, 'theirs', '', 'pending', 'medium', now(), now(), 1)
	`, strangerTask, stranger); err != nil {
		t.Fatalf("insert stranger task: %v", err)
	}

	theirs := newPostgresAttachment(t, strangerTask)
	if err := repo.Create(context.Background(), theirs, stranger); err != nil {
		t.Fatalf("Create() for the stranger unexpected error: %v", err)
	}

	mine := newPostgresAttachment(t, taskID)
	if err := repo.Create(context.Background(), mine, owner); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	got, err := repo.UnreferencedKeys(context.Background(), []string{mine.StorageKey, theirs.StorageKey})
	if err != nil {
		t.Fatalf("UnreferencedKeys() unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("UnreferencedKeys() = %v, want empty — both keys are referenced, and whose task they hang off is irrelevant here", got)
	}
}

// --- Delete ---

func TestPostgres_Delete_RemovesTheRow(t *testing.T) {
	repo, db, owner, _, taskID := newPostgresTestRepo(t)
	att := newPostgresAttachment(t, taskID)
	if err := repo.Create(context.Background(), att, owner); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if err := repo.Delete(context.Background(), att.StorageKey, owner); err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}

	if _, err := repo.FindByStorageKey(context.Background(), att.StorageKey, owner); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByStorageKey() after Delete() = %v, want ErrNotFound", err)
	}

	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM attachments`).Scan(&count); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if count != 0 {
		t.Errorf("attachments table has %d rows after Delete, want 0", count)
	}
}

// TestPostgres_Delete_OnSomeoneElsesTask_IsNotFound is the SQL half of
// the ownership rule: the DELETE ... USING tasks join must match no rows
// for a stranger, and the row must survive for the real owner.
func TestPostgres_Delete_OnSomeoneElsesTask_IsNotFound(t *testing.T) {
	repo, _, owner, stranger, taskID := newPostgresTestRepo(t)
	att := newPostgresAttachment(t, taskID)
	if err := repo.Create(context.Background(), att, owner); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if err := repo.Delete(context.Background(), att.StorageKey, stranger); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete() by a non-owner = %v, want ErrNotFound", err)
	}

	if _, err := repo.FindByStorageKey(context.Background(), att.StorageKey, owner); err != nil {
		t.Errorf("FindByStorageKey() after a refused Delete() unexpected error: %v", err)
	}
}

func TestPostgres_Delete_UnknownKey_IsNotFound(t *testing.T) {
	repo, _, owner, _, _ := newPostgresTestRepo(t)

	if err := repo.Delete(context.Background(), newUUID(t), owner); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete() error = %v, want ErrNotFound", err)
	}
}

func TestPostgres_Delete_Twice_SecondCallIsNotFound(t *testing.T) {
	repo, _, owner, _, taskID := newPostgresTestRepo(t)
	att := newPostgresAttachment(t, taskID)
	if err := repo.Create(context.Background(), att, owner); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if err := repo.Delete(context.Background(), att.StorageKey, owner); err != nil {
		t.Fatalf("first Delete() unexpected error: %v", err)
	}

	if err := repo.Delete(context.Background(), att.StorageKey, owner); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Delete() = %v, want ErrNotFound", err)
	}
}

// TestPostgres_Delete_MalformedKey_IsAQueryError documents Repository's
// own behavior when called directly with a key that isn't a syntactically
// valid UUID — the same shape TestPostgres_FindByID_MalformedID pins in
// internal/task. Whoever wires this into Service is responsible for
// isValidID (see internal/task/service.go and, once #98 lands, this
// package's own copy) running first — Repository does not, and should
// not, guess at that on its own.
func TestPostgres_Delete_MalformedKey_IsAQueryError(t *testing.T) {
	repo, _, owner, _, _ := newPostgresTestRepo(t)

	err := repo.Delete(context.Background(), "not-a-uuid", owner)
	if err == nil {
		t.Fatal("Delete() with a malformed key: expected an error, got nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("Delete() with a malformed key: got ErrNotFound, want a query/type error")
	}
}

// --- TotalBytesForUser ---

func TestPostgres_TotalBytesForUser_SumsOwnedAttachments(t *testing.T) {
	repo, _, owner, _, taskID := newPostgresTestRepo(t)

	att1 := newPostgresAttachment(t, taskID)
	att1.SizeBytes = 1000
	if err := repo.Create(context.Background(), att1, owner); err != nil {
		t.Fatalf("Create(att1) unexpected error: %v", err)
	}
	att2 := newPostgresAttachment(t, taskID)
	att2.SizeBytes = 2000
	if err := repo.Create(context.Background(), att2, owner); err != nil {
		t.Fatalf("Create(att2) unexpected error: %v", err)
	}

	total, err := repo.TotalBytesForUser(context.Background(), owner)
	if err != nil {
		t.Fatalf("TotalBytesForUser() unexpected error: %v", err)
	}
	if total != 3000 {
		t.Errorf("TotalBytesForUser() = %d, want 3000", total)
	}
}

// TestPostgres_TotalBytesForUser_NoAttachments_IsZero pins COALESCE:
// SUM() over zero rows is SQL NULL, not 0, and the query must not let
// that surface as either an error or a NULL scanned into an int64.
func TestPostgres_TotalBytesForUser_NoAttachments_IsZero(t *testing.T) {
	repo, _, owner, _, _ := newPostgresTestRepo(t)

	total, err := repo.TotalBytesForUser(context.Background(), owner)
	if err != nil {
		t.Fatalf("TotalBytesForUser() unexpected error: %v", err)
	}
	if total != 0 {
		t.Errorf("TotalBytesForUser() = %d, want 0", total)
	}
}

func TestPostgres_TotalBytesForUser_ExcludesOtherUsers(t *testing.T) {
	repo, db, owner, stranger, taskID := newPostgresTestRepo(t)
	otherTaskID := insertTask(t, db, stranger)

	mine := newPostgresAttachment(t, taskID)
	mine.SizeBytes = 500
	if err := repo.Create(context.Background(), mine, owner); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	theirs := newPostgresAttachment(t, otherTaskID)
	theirs.SizeBytes = 999999
	if err := repo.Create(context.Background(), theirs, stranger); err != nil {
		t.Fatalf("Create() for the other user unexpected error: %v", err)
	}

	total, err := repo.TotalBytesForUser(context.Background(), owner)
	if err != nil {
		t.Fatalf("TotalBytesForUser() unexpected error: %v", err)
	}
	if total != 500 {
		t.Errorf("TotalBytesForUser() = %d, want 500 (must exclude the stranger's 999999-byte file)", total)
	}
}
