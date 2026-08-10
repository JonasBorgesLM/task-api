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

-- Supports Service.ListTasks' deterministic ordering (CreatedAt ascending,
-- ties broken by ID) without a sort step as the table grows:
-- postgresRepository.FindAll issues its SELECT pre-ordered by
-- (created_at, id) so it can use this index directly instead of sorting
-- the full result set in Postgres. Service still applies its own sort on
-- the returned slice regardless — Repository makes no ordering guarantee
-- callers may rely on — so this index is a performance optimization only,
-- not a substitute for that contract.
CREATE INDEX idx_tasks_created_at_id ON tasks (created_at, id);
