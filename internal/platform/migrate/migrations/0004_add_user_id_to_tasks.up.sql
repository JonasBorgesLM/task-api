-- Adds task ownership: every task now belongs to exactly one user, and
-- every Repository query in internal/task/postgres_repository.go filters
-- by it (WHERE ... AND user_id = $N, pushed into the query for the same
-- reason pagination is — see Repository's doc comment).
--
-- NOT NULL with no default and no backfill: this is a breaking schema
-- change with no migration path for pre-existing rows, which is
-- acceptable for this project at its current (reference/dev) stage — the
-- same assumption `make seed -reset` already makes about task data being
-- disposable. Applying this migration against a tasks table that already
-- has rows will fail; truncate first (`make db-reset`) in that case.
ALTER TABLE tasks ADD COLUMN user_id UUID NOT NULL REFERENCES users(id);

-- Replaces idx_tasks_created_at_id: every FindAll query now filters by
-- user_id first (WHERE user_id = $1 ORDER BY created_at, id), so the
-- index must lead with user_id to be usable for that filter instead of
-- just the ordering.
DROP INDEX IF EXISTS idx_tasks_created_at_id;
CREATE INDEX idx_tasks_user_id_created_at_id ON tasks (user_id, created_at, id);
