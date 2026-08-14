-- Expands task.Status from {pending, done} to {pending, in_progress,
-- done, cancelled}, and adds Priority — see internal/task/service.go for
-- the legal-transition table enforced above this CHECK. As with the
-- original status column (0001_create_tasks_table.up.sql), this
-- constraint only fences the set of valid values; which transitions
-- between them are legal is Service's responsibility, not the
-- database's.
ALTER TABLE tasks DROP CONSTRAINT tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check
    CHECK (status IN ('pending', 'in_progress', 'done', 'cancelled'));

-- priority has no independent index: it is only ever read alongside a
-- user_id-scoped query, never filtered on by itself, so
-- idx_tasks_user_id_created_at_id already covers every query shape this
-- column participates in today.
ALTER TABLE tasks ADD COLUMN priority VARCHAR(10) NOT NULL DEFAULT 'medium'
    CHECK (priority IN ('low', 'medium', 'high'));
