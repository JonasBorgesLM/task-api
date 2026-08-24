-- Reverses 0005_expand_task_status_and_priority.up.sql. Any row with
-- status in_progress/cancelled or priority other than the default is
-- incompatible with the prior constraint/schema — this down migration
-- assumes none exist (same disposable-data assumption as
-- 0004_add_user_id_to_tasks.down.sql).
ALTER TABLE tasks DROP COLUMN priority;
ALTER TABLE tasks DROP CONSTRAINT tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check
    CHECK (status IN ('pending', 'done'));
