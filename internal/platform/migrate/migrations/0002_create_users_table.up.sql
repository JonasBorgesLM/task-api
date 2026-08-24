-- Creates the users table backing internal/user's postgresRepository.
--
-- Schema decisions mirror 0001_create_tasks_table.up.sql's reasoning:
--
--   - id is UUID, PRIMARY KEY, no server-side default — user.Service
--     generates it application-side, same as task.Service.newID, so
--     Repository never invents an identifier of its own.
--
--   - email is UNIQUE so Repository.CreateUser can rely on a unique
--     constraint violation (23505) to report ErrAlreadyExists, the same
--     pattern task's postgresRepository.Create already uses for task IDs.
--     Stored as given (not lowercased) — user.Service is responsible for
--     any normalization before it reaches here.
--
--   - password_hash stores a bcrypt hash only, never the plaintext
--     password. TEXT rather than a fixed VARCHAR length: bcrypt's encoded
--     output format/length is an implementation detail of the hashing
--     library, not something this schema should hardcode.
--
--   - created_at/updated_at are TIMESTAMPTZ for the same reason as tasks:
--     unambiguous UTC storage matching Go's time.Time.
CREATE TABLE users (
    id            UUID PRIMARY KEY,
    email         VARCHAR(320) NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
