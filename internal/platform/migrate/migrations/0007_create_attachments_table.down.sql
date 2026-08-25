-- Dropping the table drops idx_attachments_task_id_created_at_id and the
-- unique index behind storage_key with it, so neither needs its own
-- statement here.
--
-- This reverts the schema only. Any blobs already written under the
-- storage keys these rows held are left on disk: this migration never
-- created them and has no way to reach the filesystem the application
-- writes to. Reverting past this point therefore orphans them, and
-- reclaiming that space is an operational task, not a schema one.
DROP TABLE IF EXISTS attachments;
