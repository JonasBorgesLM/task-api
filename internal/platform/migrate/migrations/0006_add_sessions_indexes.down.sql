-- Reverses 0006_add_sessions_indexes.up.sql. Dropping these indexes only
-- costs performance (the queries they serve still return correct results
-- via sequential scan); no data is lost.
DROP INDEX IF EXISTS idx_sessions_expires_at;
DROP INDEX IF EXISTS idx_sessions_user_id;
