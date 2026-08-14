-- Creates the sessions table backing user.Service's opaque bearer-token
-- authentication (see internal/user/service.go).
--
-- Schema decisions:
--
--   - token_hash, not the raw session token, is the PRIMARY KEY: the raw
--     token is a bearer credential handed to the client once (at login)
--     and never persisted anywhere. Only sha256(token) is stored, so a
--     database leak does not itself hand out usable sessions — the same
--     reasoning as storing a bcrypt hash instead of a plaintext password
--     in 0002_create_users_table.up.sql. Using the hash as the primary
--     key (rather than a separate id column) also makes
--     Repository.FindSessionByTokenHash a primary-key lookup.
--
--   - user_id references users(id) ON DELETE CASCADE so a deleted user
--     can never leave orphaned sessions behind.
--
--   - expires_at is enforced by user.Service.ValidateToken, not by the
--     database (no scheduled job deletes expired rows here): an expired
--     session is simply rejected on next use and lazily deleted then.
--     This mirrors task's status CHECK constraint, which fences the value
--     set but leaves transition/expiry *rules* to Service.
CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
