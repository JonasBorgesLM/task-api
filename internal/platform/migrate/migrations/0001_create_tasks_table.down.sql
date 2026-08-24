-- Reverses 0001_create_tasks_table.up.sql. Dropping the table also drops
-- idx_tasks_created_at_id and the status CHECK constraint with it.
DROP TABLE IF EXISTS tasks;
