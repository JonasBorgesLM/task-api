# Task API

A small HTTP REST API for task management, built with Go.

## Overview

Task API provides a simple interface for creating and managing tasks. Each task has a title, an optional description, and a status (pending or done). The API supports full lifecycle management: create, read, update, complete, and delete.

## Architecture

The application is organized in three layers with explicit dependency injection. Each layer depends only on the layer below it through interfaces.

```
HTTP Request
     │
     ▼
┌─────────────┐
│   Handler   │  Decodes HTTP requests, calls Service, encodes responses
└──────┬──────┘
       │ calls
       ▼
┌─────────────┐
│   Service   │  Validates input, applies business rules, manages timestamps and IDs
└──────┬──────┘
       │ calls
       ▼
┌─────────────┐
│ Repository  │  Persists and retrieves tasks — in-memory or PostgreSQL (see below)
└─────────────┘
```

`cmd/api/main.go` acts as the **Composition Root**: it instantiates all concrete types and wires dependencies together. No layer instantiates its own dependencies — critically, `Service` and `Handler` depend only on the `Repository` interface (`task/repository.go`) and are completely unaware that PostgreSQL exists. Only `cmd/api/main.go` imports a PostgreSQL package; see [PostgreSQL](#postgresql) below.

## Requirements

- Go 1.26 or later, matching the version pinned in `go.mod` (the codebase and its tests use `http.ServeMux` path parameters, `log/slog`, range-over-int, and `sync.WaitGroup.Go`)
- No external dependencies for the core application logic (`task`, `config`, `middleware`) or for running with the in-memory store. The only external dependency in the whole module is [`github.com/jackc/pgx/v5`](https://github.com/jackc/pgx), used solely to talk to PostgreSQL when `DATABASE_URL` is configured — see [PostgreSQL](#postgresql).
- [Docker](https://www.docker.com/) (optional), to run a local PostgreSQL instance via `docker-compose.yml`

## Getting Started

### Clone

```bash
git clone https://github.com/JonasBorgesLM/task-api.git
cd task-api
```

### Configure

Copy the example env file and adjust it to your local setup:

```bash
cp .env.example .env
```

`.env` is read automatically on startup (see [Configuration](#configuration) below). It is listed in `.gitignore` and must **never** be committed — `.env.example` is the only env file that belongs in version control, and it must only ever contain non-sensitive placeholder values.

If you don't create a `.env` file, the application falls back to its built-in defaults (`HTTP_ADDR=:8080`, 5s/10s/60s read/write/idle timeouts, `HTTP_SHUTDOWN_TIMEOUT=10s`, `LOG_LEVEL=info`).

### Run

```bash
go run ./cmd/api
```

The server starts on port `8080` by default:

```
{"time":"...","level":"INFO","msg":"server started","addr":":8080"}
```

### Run with custom configuration

Environment variables already set in the shell always take precedence over `.env` — use this to override a value for a single run without editing the file:

```bash
HTTP_ADDR=:9090 HTTP_SHUTDOWN_TIMEOUT=30s go run ./cmd/api
```

### Build

```bash
go build -o task-api ./cmd/api
./task-api
```

### Tests

```bash
# Run all tests
go test ./...

# Run with race detector
go test -race ./...
```

## Configuration

All configuration is read from environment variables. If a variable is not set, the default value is used.

On startup, the application loads `.env` from the working directory (if present) and applies any variable defined there that isn't already set in the shell environment — see [Configure](#configure) above. Real environment variables always win over `.env`, which makes `.env` safe to use for local defaults while still letting deployment environments (CI, containers, etc.) override everything explicitly.

| Variable | Description | Default | Example |
|---|---|---|---|
| `HTTP_ADDR` | TCP address the HTTP server listens on (`host:port`) | `:8080` | `HTTP_ADDR=:9090` |
| `HTTP_READ_TIMEOUT` | `http.Server.ReadTimeout` | `5s` | `HTTP_READ_TIMEOUT=10s` |
| `HTTP_WRITE_TIMEOUT` | `http.Server.WriteTimeout` | `10s` | `HTTP_WRITE_TIMEOUT=15s` |
| `HTTP_IDLE_TIMEOUT` | `http.Server.IdleTimeout` | `60s` | `HTTP_IDLE_TIMEOUT=120s` |
| `HTTP_SHUTDOWN_TIMEOUT` | Maximum time to wait for in-flight requests during shutdown | `10s` | `HTTP_SHUTDOWN_TIMEOUT=30s` |
| `LOG_LEVEL` | Minimum log level: `debug`, `info`, `warn`, or `error` | `info` | `LOG_LEVEL=debug` |
| `DOTENV_PATH` | Path to the `.env` file `Load` reads before the OS environment | `.env` | `DOTENV_PATH=/etc/task-api/.env` |
| `DATABASE_URL` | PostgreSQL connection string. Unset means "use the in-memory store" — see [PostgreSQL](#postgresql) | *(unset)* | `DATABASE_URL=postgres://user:pass@host:5432/dbname?sslmode=disable` |
| `DB_MAX_OPEN_CONNS` | Maximum open connections in the database pool (`sql.DB.SetMaxOpenConns`) | `25` | `DB_MAX_OPEN_CONNS=50` |
| `DB_MAX_IDLE_CONNS` | Maximum idle connections kept in the database pool (`sql.DB.SetMaxIdleConns`) | `25` | `DB_MAX_IDLE_CONNS=10` |
| `DB_CONN_MAX_LIFETIME` | Maximum lifetime of a pooled database connection (`sql.DB.SetConnMaxLifetime`) | `5m` | `DB_CONN_MAX_LIFETIME=15m` |
| `DB_AUTO_MIGRATE` | Whether to apply pending PostgreSQL migrations on startup | `true` | `DB_AUTO_MIGRATE=false` |

**Validation rules:**

- `HTTP_ADDR` must be a syntactically valid `host:port` address (per `net.SplitHostPort`) with a numeric port between 1 and 65535. Port 0 is rejected.
- All `HTTP_*_TIMEOUT` and `DB_CONN_MAX_LIFETIME` variables must be a valid Go duration string (e.g. `10s`, `1m`, `500ms`) and must be strictly positive.
- `LOG_LEVEL` is case-insensitive and must be one of `debug`, `info`, `warn`/`warning`, or `error`.
- `DB_MAX_OPEN_CONNS` and `DB_MAX_IDLE_CONNS` must be positive integers.
- `DB_AUTO_MIGRATE` must be a valid boolean (`true`/`false`/`1`/`0`/...).
- `DATABASE_URL` is *not* format-validated by `config.Load` — connection string formats vary, and the PostgreSQL driver is the authority on what it accepts. An invalid value surfaces at startup, with the driver's own error, when the application tries to open and ping the connection (see [PostgreSQL](#postgresql)).

## PostgreSQL

`Repository` (`task/repository.go`) has two implementations:

- **`memoryRepository`** (`task/memory_repository.go`) — an in-memory, mutex-protected map. Used automatically whenever `DATABASE_URL` is unset, including every existing unit and integration test that never sets it. Kept intentionally: it needs no external service, which makes `Service`/`Handler`/`Repository`-contract tests fast and hermetic.
- **`postgresRepository`** (`task/postgres_repository.go`) — backed by PostgreSQL, used automatically whenever `DATABASE_URL` is set.

`cmd/api/main.go`'s `buildRepository` is the *only* place that chooses between them. `Service` and `Handler` are constructed against the `Repository` interface either way and cannot tell which implementation they're talking to.

### Library choice

`postgresRepository` is built on the standard library's `database/sql` (`*sql.DB`), using [`github.com/jackc/pgx/v5`](https://github.com/jackc/pgx) (specifically its `stdlib` subpackage) purely as the registered `database/sql` driver. This was chosen over the alternatives for concrete reasons:

- **vs. `lib/pq`** — `lib/pq` is in maintenance mode (the README itself states it is "not accepting new features" and recommends `pgx`); `pgx` is the actively maintained, de facto standard PostgreSQL driver for Go today.
- **vs. using `pgx`'s own native pool/interface directly (`pgxpool.Pool`)** — that API is arguably more idiomatic *pgx*, but it isn't `*sql.DB`, and this project's connection-management requirements (pooling, `Ping`, graceful `Close`) are fully met by the standard library's own connection-pooling primitives (`SetMaxOpenConns`/`SetMaxIdleConns`/`SetConnMaxLifetime`). Going through `database/sql` keeps `postgresRepository` written against a standard, widely understood interface (`*sql.DB`, `*sql.Tx`, `context`-aware `...Context` methods) rather than a driver-specific one, while `pgx` still does the actual wire-protocol work underneath.
- **Pure Go, no cgo** — `pgx` implements the PostgreSQL wire protocol itself; it needs no `libpq` C library. That matters concretely here: the production `Dockerfile` builds a static (`CGO_ENABLED=0`) binary into a `scratch` image with no libc at all, and `pgx` doesn't break that.
- **Dependency footprint** — adding `pgx` pulls in only its own small dependency tree (`pgpassfile`, `pgservicefile`, `golang.org/x/text`), all from the same maintainers or the Go team; there is no sprawling transitive dependency graph.

Migrations use a small embedded runner (`task/postgres_migrate.go`) rather than adding a dependency like `golang-migrate` for a single table: `RunMigrations` embeds `task/migrations/*.up.sql` via `embed.FS`, tracks applied versions in a `schema_migrations` table, and applies each pending migration inside its own transaction. The `.up.sql`/`.down.sql` naming still follows the conventional migration-tool format, so the same files work unmodified if the project ever outgrows this and adopts a dedicated migration tool.

### Schema

See `task/migrations/0001_create_tasks_table.up.sql` for the full, commented DDL. Summary of the decisions:

| Column | Type | Why |
|---|---|---|
| `id` | `UUID PRIMARY KEY` | Matches `Task.ID`. Has no server-side default (no `gen_random_uuid()`): `Service.newID` generates it application-side (see `task/service.go`), and `Repository` never invents identifiers of its own — that would be business logic leaking into storage. |
| `title` | `VARCHAR(200) NOT NULL` | Mirrors `task.maxTitleLen`. A structural backstop against a malformed write reaching storage from outside `Service` — `Service` itself is still what enforces this on the way in. |
| `description` | `VARCHAR(2000) NOT NULL DEFAULT ''` | Mirrors `task.maxDescriptionLen`, same reasoning as `title`. |
| `status` | `VARCHAR(20) NOT NULL DEFAULT 'pending'` with `CHECK (status IN ('pending', 'done'))` | Keeps `Status` a genuinely controlled state at the storage layer, matching `task.Status`'s only two values today. The `CHECK` fences which *values* are valid; it does not encode which *transitions* are legal (e.g. `CompleteTask`'s idempotency) — that stays entirely in `Service`. |
| `created_at`, `updated_at` | `TIMESTAMPTZ NOT NULL` | Stored unambiguously in UTC regardless of the server's local timezone setting. `Service` sets both on every write; the column defaults only cover a row inserted outside `Service`. |
| `version` | `INTEGER NOT NULL DEFAULT 1` | Backs `Repository`'s optimistic-concurrency contract (see the `Repository` interface's doc comment) — starts at 1 on insert, incremented on every successful `Update`. Never part of the JSON wire format. |

### Indexes

- The `id` primary key already provides the index `FindByID`/`Update`/`Delete` need.
- `idx_tasks_created_at_id` on `(created_at, id)` supports `FindAll`'s `ORDER BY created_at, id` — the same tie-break `Service.ListTasks` applies — without a sort step in Postgres as the table grows. This is the *only* extra index added, and deliberately so: the current API has no other query shape (e.g. no filter by `status`) that would benefit from one, and an index only earns its write-amplification and storage cost when a real query needs it. If `GET /tasks` grows a `status` filter later, an index on `status` (or a partial index per status) would be added at that point.

### Transactions

`Update` is the one method that needs more than a single statement, because of the optimistic-concurrency contract: read the current `Version`, compare it against the caller's, and only then write. Run as three separate round-trips, two concurrent `Update` calls could both read the same `Version`, both pass the comparison, and the second write would silently clobber the first (a lost update) — exactly the bug `Version` exists to prevent.

`postgresRepository.Update` closes that race with a transaction: `SELECT version FROM tasks WHERE id = $1::uuid FOR UPDATE` takes a row-level lock for the transaction's duration, so a second concurrent transaction's own `SELECT ... FOR UPDATE` on the same row blocks until the first commits or rolls back. By the time it proceeds, it observes the already-incremented `Version` and correctly returns `ErrConflict` instead of racing. `TestPostgres_ConcurrentUpdate_LosersGetErrConflict` (`task/postgres_repository_test.go`) drives real concurrent goroutines through this path to verify it.

`RunMigrations` (`task/postgres_migrate.go`) also runs each migration's DDL and its `schema_migrations` bookkeeping insert inside one transaction, so a crash or error partway through can never leave the schema changed but unrecorded (which would cause it to be silently reapplied) or recorded without actually being applied.

### Error handling

`postgresRepository` translates PostgreSQL-specific failures into the same sentinel errors (`ErrNotFound`, `ErrAlreadyExists`, `ErrConflict` — `task/errors.go`) `memoryRepository` already returns, so `Service` and `Handler` need no PostgreSQL-specific branch anywhere:

- `sql.ErrNoRows` (from `FindByID`, or from `Update`'s `SELECT ... FOR UPDATE`) becomes `ErrNotFound`.
- A unique-constraint violation on `Create` (PostgreSQL SQLSTATE `23505`, detected via `errors.As` into `*pgconn.PgError`) becomes `ErrAlreadyExists`.
- A `Version` mismatch inside `Update`'s transaction becomes `ErrConflict`.
- Any other database error (connection failure, timeout, ...) is wrapped with `fmt.Errorf("postgres: ...: %w", err)` and returned as-is — `Handler.handleServiceError` maps anything it doesn't recognize to a generic HTTP `500`, logging the wrapped error server-side but never exposing driver internals to the client.

### SQL injection

Every query in `postgresRepository` uses `database/sql` placeholders (`$1`, `$2`, ...) — no request-derived value (title, description, ID) is ever concatenated into SQL text. `id` values are additionally cast on the Postgres side (`$1::uuid`) so a malformed ID is rejected by the type system rather than silently mismatching at the wire level. `TestPostgres_Create_RejectsSQLInjectionAttempt` (`task/postgres_repository_test.go`) creates a task whose title is a `DROP TABLE` injection attempt and asserts it's stored as inert text and the table survives.

### Running against PostgreSQL locally

```bash
# Start a local Postgres (docker-compose.yml)
docker compose up -d postgres
# or: make db-up

# Run the API against it — migrations apply automatically on startup
DATABASE_URL="postgres://task_api:task_api@localhost:5432/task_api?sslmode=disable" go run ./cmd/api

# Run postgresRepository's own integration test suite against it
TEST_DATABASE_URL="postgres://task_api:task_api@localhost:5432/task_api?sslmode=disable" go test ./task/... -run Postgres -v
# or: make test-postgres
```

`TEST_DATABASE_URL` is deliberately a different variable from `DATABASE_URL`: the test suite `TRUNCATE`s the `tasks` table before every test, so it should never accidentally point at a database with data you care about.

## API

All endpoints accept and return `application/json`.

---

### `POST /tasks`

Creates a new task.

**Request body**

```json
{
  "title": "Buy groceries",
  "description": "Milk, eggs, bread"
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `title` | string | yes | Task title. Cannot be empty or whitespace-only after trimming; at most 200 characters. |
| `description` | string | no | Optional task description; at most 2000 characters. |

Both fields are trimmed of leading/trailing whitespace before being stored.

**Response** `201 Created`

```json
{
  "id": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "title": "Buy groceries",
  "description": "Milk, eggs, bread",
  "status": "pending",
  "created_at": "2026-08-08T12:00:00Z",
  "updated_at": "2026-08-08T12:00:00Z"
}
```

**HTTP status codes**

| Code | Reason |
|---|---|
| `201` | Task created successfully |
| `400` | Missing or empty title, or malformed JSON |
| `409` | A task with the generated ID already exists (practically unreachable — IDs are server-generated UUIDv4) |
| `500` | Unexpected server error |

---

### `GET /tasks`

Returns tasks ordered by `created_at` ascending (oldest first, ties broken by `id`). Returns an empty array when no tasks exist.

**Query parameters** (both optional; omitting both returns everything, unchanged from before pagination existed)

| Parameter | Type | Description |
|---|---|---|
| `limit` | integer ≥ 0 | Maximum number of tasks to return. |
| `offset` | integer ≥ 0 | Number of tasks to skip before collecting `limit` results. |

**Response** `200 OK`

```json
[
  {
    "id": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
    "title": "Buy groceries",
    "description": "Milk, eggs, bread",
    "status": "pending",
    "created_at": "2026-08-08T12:00:00Z",
    "updated_at": "2026-08-08T12:00:00Z"
  }
]
```

**HTTP status codes**

| Code | Reason |
|---|---|
| `200` | Success (empty array `[]` if no tasks exist, or if `offset` is past the end of the list) |
| `400` | `limit` or `offset` is present but not a non-negative integer |
| `500` | Unexpected server error |

---

### `GET /tasks/{id}`

Returns a single task by ID.

**Response** `200 OK`

```json
{
  "id": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "title": "Buy groceries",
  "description": "Milk, eggs, bread",
  "status": "pending",
  "created_at": "2026-08-08T12:00:00Z",
  "updated_at": "2026-08-08T12:00:00Z"
}
```

**HTTP status codes**

| Code | Reason |
|---|---|
| `200` | Task found |
| `404` | No task with the given ID |
| `500` | Unexpected server error |

---

### `PUT /tasks/{id}`

Updates the title and description of an existing task. `CreatedAt`, `Status`, and `ID` are not affected.

**Request body**

```json
{
  "title": "Buy groceries and cook dinner",
  "description": "Don't forget olive oil"
}
```

Same field constraints as `POST /tasks` (see above): `title` required, at most 200 characters; `description` optional, at most 2000 characters; both trimmed before being stored.

**Response** `200 OK` — the updated task.

**HTTP status codes**

| Code | Reason |
|---|---|
| `200` | Task updated |
| `400` | Missing/empty/overlong title, overlong description, or malformed JSON |
| `404` | No task with the given ID |
| `409` | The task was modified by another request between your read and this write (optimistic concurrency conflict) — re-fetch the task and retry |
| `500` | Unexpected server error |

---

### `PATCH /tasks/{id}/done`

Marks a task as done. This operation is **idempotent**: calling it on an already-completed task returns the task unchanged without modifying `UpdatedAt`.

No request body required.

**Response** `200 OK` — the task with `status: "done"`.

```json
{
  "id": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "title": "Buy groceries",
  "description": "Milk, eggs, bread",
  "status": "done",
  "created_at": "2026-08-08T12:00:00Z",
  "updated_at": "2026-08-08T12:05:00Z"
}
```

**HTTP status codes**

| Code | Reason |
|---|---|
| `200` | Task marked as done (or was already done) |
| `404` | No task with the given ID |
| `409` | The task was modified by another request between your read and this write (optimistic concurrency conflict) — re-fetch the task and retry. Never returned for a task that was already `done`, since that path never writes. |
| `500` | Unexpected server error |

---

### `DELETE /tasks/{id}`

Removes a task permanently.

No request body. No response body.

**HTTP status codes**

| Code | Reason |
|---|---|
| `204` | Task deleted |
| `404` | No task with the given ID |
| `500` | Unexpected server error |

---

### `GET /health`

Returns the application health status. Always responds `200 OK` while the server is running.

**Response** `200 OK`

```json
{
  "status": "ok"
}
```

**HTTP status codes**

| Code | Reason |
|---|---|
| `200` | Application is healthy |

---

### `GET /debug/vars`

Standard library `expvar` endpoint: exposes baseline runtime observability (command line, memory stats, GC stats, goroutine-related counters) as JSON. Not part of the Task domain API — provided for basic operational visibility without pulling in an external metrics dependency.

## Error Handling

All error responses use the same JSON envelope:

```json
{
  "error": "description of the problem"
}
```

| HTTP Code | Meaning |
|---|---|
| `400 Bad Request` | Invalid input — malformed JSON, failed business rule validation (e.g. empty or overlong title), or an invalid `limit`/`offset` on `GET /tasks`. The error message describes the specific problem. |
| `404 Not Found` | The requested task ID does not exist. |
| `409 Conflict` | Either a task with the generated ID already exists on `POST /tasks` (practically unreachable, since IDs are server-generated UUIDv4), or `PUT /tasks/{id}` / `PATCH /tasks/{id}/done` lost an optimistic concurrency race against another concurrent write to the same task — re-fetch and retry. |
| `500 Internal Server Error` | An unexpected server-side failure. The response body contains a generic message; details are logged server-side and never exposed to the client. |

## Development

```bash
# Run all tests
go test ./...

# Run with race detector (recommended before committing)
go test -race ./...

# Format all source files
gofmt -w .

# Run static analysis
go vet ./...
```

A `Makefile` is provided to standardise common commands:

```bash
make run        # start the server
make test       # run all tests
make test-race  # run all tests with race detector
make fmt        # format all source files
make vet        # run static analysis
make check      # fmt + vet + test-race (run before committing)
```

## Project Structure

```
task-api/
├── cmd/
│   └── api/
│       ├── main.go               # Composition Root: wires dependencies and starts the server
│       ├── health.go             # GET /health handler
│       ├── main_test.go          # Tests for server lifecycle and health endpoint
│       └── main_integration_test.go  # Full-stack (real Repository+Service+Handler) HTTP test
├── docker-compose.yml    # Local PostgreSQL instance for development/testing
├── go.mod
├── config/
│   ├── config.go         # Environment variable loading and validation
│   └── config_test.go
└── task/
    ├── task.go           # Domain model: Task struct and Status type
    ├── errors.go         # Domain error sentinels (ErrNotFound, ErrInvalidInput, ErrAlreadyExists, ErrConflict)
    ├── repository.go     # Repository interface
    ├── memory_repository.go      # In-memory implementation of Repository (with CAS-based optimistic concurrency)
    ├── memory_repository_test.go
    ├── postgres_repository.go    # PostgreSQL implementation of Repository (*sql.DB, transactions, parameterized queries)
    ├── postgres_repository_test.go  # Integration tests against a real PostgreSQL instance (skipped without TEST_DATABASE_URL)
    ├── postgres_migrate.go       # Embedded migration runner (RunMigrations)
    ├── migrations/
    │   ├── 0001_create_tasks_table.up.sql
    │   └── 0001_create_tasks_table.down.sql
    ├── service.go        # Business logic: validation, ID generation, timestamps, ordering
    ├── service_test.go
    ├── handler.go        # HTTP handlers, route registration, pagination
    └── handler_test.go
```

## Design Decisions

**Service contains all business rules**
Validation (non-empty title), ID generation, and timestamp management live exclusively in the `Service`. The `Handler` translates HTTP; the `Repository` stores data. Neither layer contains domain logic.

**Repository is interface-based**
`Repository` is defined as a Go interface. `cmd/api/main.go`'s `buildRepository` picks the concrete implementation at startup — `memoryRepository` or `postgresRepository`, based solely on whether `DATABASE_URL` is set — and injects it into `Service`. Adding `postgresRepository` required zero changes to `Service` or `Handler`; see [PostgreSQL](#postgresql) for the implementation itself.

**UUID generated in the Service**
The `Service` generates a UUID v4 using `crypto/rand` from the standard library. The `Repository` receives a fully-formed `Task` and never assigns IDs. This keeps ID generation testable and independent of storage.

**Timestamps managed by the Service**
`CreatedAt` is set once at creation and never modified. `UpdatedAt` is refreshed on every mutation. The HTTP client cannot set or override either field — the request structs simply do not include them.

**`CompleteTask` is idempotent**
Calling `PATCH /tasks/{id}/done` on a task that is already `done` returns the current task without modifying `UpdatedAt` or calling `Repository.Update`. This follows REST conventions and makes the operation safe to retry.

**`MemoryRepository` is thread-safe**
The in-memory store uses `sync.RWMutex`. Multiple concurrent reads are allowed; writes are exclusive. All methods are safe to call from concurrent goroutines.

**`MemoryRepository` is kept alongside `PostgresRepository`, not replaced**
`memoryRepository` remains the default whenever `DATABASE_URL` is unset (including every `Service`/`Handler` unit and integration test), since it needs no external service and keeps that test suite fast and hermetic. `postgresRepository` is what production deployments are expected to configure via `DATABASE_URL`. Both satisfy the exact same `Repository` contract — see [PostgreSQL](#postgresql).

**Domain errors are translated at the Handler layer**
`ErrNotFound` becomes HTTP 404. `ErrInvalidInput` becomes HTTP 400 with the original message. `ErrAlreadyExists` becomes HTTP 409 (practically unreachable, since IDs are server-generated UUIDv4). `ErrConflict` also becomes HTTP 409 (an optimistic concurrency conflict — see below). All other errors become HTTP 500 with a generic message, and the original error is logged server-side.

**Structured logging with `log/slog`**
The application uses `log/slog` (Go standard library) with a JSON handler. The logger is created in `cmd/api/main.go` and injected into the `Handler`. No global mutable logger exists.

**`context.Context` propagates from the HTTP request through Service and Repository**
Every `Repository` and `Service` method takes `ctx context.Context` as its first parameter, sourced from `r.Context()` in the `Handler`. The in-memory `Repository` checks `ctx.Err()` before each operation. `postgresRepository` passes the same `ctx` all the way down to every `*sql.DB`/`*sql.Tx` call (`QueryRowContext`, `ExecContext`, `BeginTx`, ...), so a canceled request or expired deadline aborts the in-flight query against PostgreSQL too, instead of running it to completion for a client that already gave up.

**Request bodies are capped at 1 MiB**
`POST /tasks` and `PUT /tasks/{id}` wrap the request body in `http.MaxBytesReader` before decoding JSON, so an oversized payload is rejected instead of being fully buffered into memory.

**Optimistic concurrency control on updates**
`Task` carries an internal `Version` field (`json:"-"`, never exposed on the wire). `UpdateTask` and `CompleteTask` perform a read-modify-write against the `Repository` (`FindByID` then `Update`), passing back the `Version` they read. `memoryRepository.Update` rejects the write with `ErrConflict` (surfaced as HTTP `409`) if the stored `Version` no longer matches — i.e. another writer updated the task in between — instead of silently overwriting it (a "lost update"). `Create` always resets `Version` to `1` regardless of what the caller supplies, and a successful `Update` increments it. Callers that receive a `409` should re-fetch the task and retry.

**`GET /tasks` ordering is deterministic**
`memoryRepository.FindAll` iterates a Go map, whose iteration order is randomized. `Service.ListTasks` sorts the result by `CreatedAt` ascending (ties broken by `ID`) before returning it, so callers — including paginated ones — see a stable order across requests rather than depending on map iteration.

## Graceful Shutdown

The server handles `SIGINT` (Ctrl+C) and `SIGTERM` (sent by process managers and container runtimes):

1. The signal is received on a buffered channel via `signal.Notify`.
2. `http.Server.Shutdown(ctx)` is called with a context bounded by `HTTP_SHUTDOWN_TIMEOUT`.
3. The server stops accepting new connections immediately.
4. In-flight requests are allowed to complete.
5. If the timeout expires before all requests finish, `Shutdown` returns an error, which is logged and propagates to the process exit code.
6. A clean shutdown logs `server stopped cleanly` and exits with code `0`.

## Future Improvements

The following are possible improvements not yet implemented:

- **Authentication and authorization** — protect endpoints with JWT or API key middleware
- **Application-level metrics** — `GET /debug/vars` exposes baseline Go runtime stats via `expvar`; a richer metrics surface (request count/latency/error-rate per route) would still need dedicated instrumentation, e.g. Prometheus
- **Distributed tracing** — integrate OpenTelemetry for end-to-end request tracing
- **Task filtering** — filter tasks by status in `GET /tasks` (would also be the point to add an index on `status` to `postgresRepository`'s schema — see [PostgreSQL](#postgresql))
- **Dedicated migration step for multi-replica deployments** — `DB_AUTO_MIGRATE=true` (the default) is convenient for a single instance, but running several replicas that each try to migrate on boot is better served by disabling it and running `task.RunMigrations` as a separate deploy step instead
