-- Creates the tasks table backing postgresRepository (task/postgres_repository.go).
--
-- Schema decisions:
--
--   - id is UUID, PRIMARY KEY. It has no server-side default (no
--     gen_random_uuid()): Service.newID generates it application-side and
--     Repository.Create receives an already-populated Task, so the
--     Repository must never invent an identifier of its own.
--
--   - title/description lengths mirror task.maxTitleLen/maxDescriptionLen
--     (200/2000 — see task/service.go) so a malformed write from outside
--     Service (a bug, a direct SQL client) is rejected by the database
--     itself rather than silently truncated or stored oversized.
--     Validating length is Service's job on the way in; this is a
--     structural backstop, not a duplicate of that business rule.
--
--   - status is constrained to today's task.Status values (pending, done)
--     so storage can never hold a value Service and Handler don't know how
--     to interpret. Which transitions between those values are legal
--     (e.g. CompleteTask's idempotency) is still decided entirely by
--     Service — this CHECK only fences the set of valid states, it does
--     not encode transition rules.
--
--   - created_at/updated_at are TIMESTAMPTZ so values are stored
--     unambiguously in UTC regardless of the server's local timezone
--     setting, matching Go's time.Time (always UTC-normalized on the wire
--     by encoding/json). Service sets both on write; the column defaults
--     only cover the pathological case of a row inserted outside Service.
--
--   - version backs Repository's optimistic-concurrency contract (see the
--     doc comment on the Repository interface in task/repository.go): it
--     starts at 1 on insert and is incremented by every successful
--     Update. It is never part of the JSON wire format.
CREATE TABLE tasks (
    id          UUID PRIMARY KEY,
    title       VARCHAR(200) NOT NULL,
    description VARCHAR(2000) NOT NULL DEFAULT '',
    status      VARCHAR(20) NOT NULL DEFAULT 'pending',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    version     INTEGER NOT NULL DEFAULT 1,
    CONSTRAINT tasks_status_check CHECK (status IN ('pending', 'done'))
);

-- Supports the deterministic ordering (created_at ascending, ties broken
-- by id) that Repository.FindAll guarantees: it issues its SELECT
-- pre-ordered by (created_at, id) so PostgreSQL can walk this index
-- directly instead of sorting the full result set as the table grows.
--
-- Historical note: when this migration was written, ordering was applied
-- by Service on the returned slice and Repository promised nothing about
-- it, making this index a pure optimization. That contract has since
-- moved — ordering (and the LIMIT/OFFSET window) is now Repository's
-- responsibility, so the ORDER BY this index serves is load-bearing, not
-- decorative. See the Repository interface's doc comment in
-- internal/task/repository.go for the current contract; 0004 supersedes
-- this index itself with a user_id-leading one.
CREATE INDEX idx_tasks_created_at_id ON tasks (created_at, id);
