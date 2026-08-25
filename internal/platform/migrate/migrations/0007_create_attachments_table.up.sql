-- Creates the attachments table backing internal/attachment's
-- postgresRepository.
--
-- Schema decisions:
--
--   - id is UUID, PRIMARY KEY, with no server-side default, for the same
--     reason tasks.id has none: Service generates it application-side and
--     Repository receives an already-populated Attachment, so storage
--     never invents an identifier of its own.
--
--   - task_id is the ownership chain. An attachment has no user_id of its
--     own: it belongs to a task, and that task belongs to a user, so
--     duplicating the owner here would create a second copy of a fact
--     that can then disagree with the first. Every Repository query joins
--     through tasks to filter by owner instead (see
--     internal/attachment/postgres_repository.go).
--
--     ON DELETE CASCADE because an attachment cannot outlive its task —
--     there would be no path left to reach it, and no owner to authorize
--     that reach. NOTE that this cascades the *rows* only; the blobs on
--     disk are not touched by it. See internal/attachment/repository.go.
--
--   - storage_key is the name the file is written under, generated
--     server-side and never derived from what the client called the file.
--     It is UUID rather than TEXT so the type itself rejects anything
--     that could be a path: no separators, no dots, no traversal
--     sequence survives a UUID parse. UNIQUE both because two rows must
--     never point at one blob and because the resulting index is what
--     serves FindByStorageKey, the per-download lookup.
--
--   - original_filename is metadata and nothing else. It is what the
--     client called the file, retained so a download can offer a
--     meaningful name back, and it is never used to build a path. 255 is
--     the limit essentially every filesystem imposes on a single name
--     component, so a longer value could not have been a real filename
--     on the uploader's machine either.
--
--   - content_type is what the upload declared, bounded by the same
--     255 as the filename. It is stored so the download can echo it, not
--     trusted as a statement of fact about the bytes.
--
--   - size_bytes is BIGINT: INTEGER caps at 2 GiB, which is a limit the
--     column has no business imposing. The CHECK exists because a
--     negative size is not a small file, it is a bug that would otherwise
--     be persisted silently.
--
--   - created_at is TIMESTAMPTZ, matching every other timestamp in this
--     schema (see 0001_create_tasks_table.up.sql for the reasoning).
CREATE TABLE attachments (
    id                UUID PRIMARY KEY,
    task_id           UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    original_filename VARCHAR(255) NOT NULL,
    storage_key       UUID NOT NULL UNIQUE,
    content_type      VARCHAR(255) NOT NULL,
    size_bytes        BIGINT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT attachments_size_bytes_check CHECK (size_bytes >= 0)
);

-- Serves two access paths that would otherwise both be sequential scans.
-- PostgreSQL does not index a foreign key's referencing column
-- automatically — the same omission 0006_add_sessions_indexes.up.sql was
-- written to correct — so without this:
--
--   - FindByTask (WHERE task_id = $1 ORDER BY created_at, id) reads the
--     whole table on every call.
--   - Deleting a task scans all of attachments to find the rows its
--     ON DELETE CASCADE has to remove.
--
-- Composite rather than a bare task_id index because FindByTask filters
-- *and* orders, exactly like idx_tasks_user_id_created_at_id: leading
-- with task_id makes it usable for the filter, and the trailing columns
-- let the ordering come from the index instead of a sort.
CREATE INDEX idx_attachments_task_id_created_at_id ON attachments (task_id, created_at, id);
