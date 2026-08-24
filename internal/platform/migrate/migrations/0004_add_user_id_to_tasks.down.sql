-- Reverses 0004_add_user_id_to_tasks.up.sql.
DROP INDEX IF EXISTS idx_tasks_user_id_created_at_id;
CREATE INDEX idx_tasks_created_at_id ON tasks (created_at, id);
ALTER TABLE tasks DROP COLUMN user_id;
