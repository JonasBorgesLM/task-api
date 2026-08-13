# Task API

A small, production-shaped HTTP REST API for task management, written in Go. It exists as a compact reference for how a real Go service is put together: a strict layered architecture, a swappable persistence layer (in-memory and PostgreSQL, behind one interface), structured logging, graceful shutdown, and a test suite that keeps unit and integration tests genuinely separate.

## Table of Contents

1. [Overview](#overview)
2. [Architecture](#architecture)
3. [Project Structure](#project-structure)
4. [Requirements](#requirements)
5. [Configuration](#configuration)
6. [Running Locally](#running-locally)
7. [Running with Docker](#running-with-docker)
8. [Testing](#testing)
9. [Race Detector](#race-detector)
10. [Coverage](#coverage)
11. [Migrations](#migrations)
12. [API](#api)
13. [Example Requests](#example-requests)
14. [Error Handling](#error-handling)
15. [Graceful Shutdown](#graceful-shutdown)
16. [Observability](#observability)
17. [Design Decisions](#design-decisions)
18. [Future Improvements](#future-improvements)

## Overview

Task API manages the lifecycle of a task: a title, an optional description, and a status (`pending` or `done`). It exposes that as a small JSON REST API — create, read (single and list, with pagination), update, mark-complete, and delete — over HTTP.

The functional surface is intentionally small. The point of the project is what's *underneath* it: a layered architecture where each layer depends only on an interface, not a concrete implementation; a persistence layer that can be swapped between an in-memory store and PostgreSQL without either the business logic or the HTTP layer knowing which one is in use; explicit configuration and dependency wiring with no hidden globals; and a development workflow (Docker, migrations, CI, tests) that mirrors how this would actually be operated.

## Architecture

The application is organized in three layers with explicit dependency injection. Each layer depends only on the layer *below* it, and only through an interface — never a concrete type from another layer.

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
│ Repository  │  Persists and retrieves tasks — interface only
└──────┬──────┘
       │ implemented by
       ├──────────────────────┐
       ▼                      ▼
┌──────────────┐     ┌──────────────────┐
│memoryRepository│    │ postgresRepository │
│ (in-process,   │    │ (*sql.DB, real     │
│  no external   │    │  PostgreSQL)       │
│  dependency)   │    │                    │
└──────────────┘     └──────────────────┘
```

`cmd/api/main.go` is the **Composition Root**: it is the only place that instantiates concrete types and wires them together. No layer instantiates its own dependencies — they're all passed in. This has one consequence worth calling out explicitly: **`Service` and `Handler` depend only on the `Repository` interface (`task/repository.go`) and are completely unaware that PostgreSQL exists.** Only `cmd/api/main.go` (and `cmd/migrate/main.go`, a separate small binary — see [Migrations](#migrations)) import a PostgreSQL package. Swapping `memoryRepository` for `postgresRepository` — or adding a third implementation later — requires zero changes to business logic or HTTP handling.

## Project Structure

```
task-api/
├── cmd/
│   ├── api/
│   │   ├── main.go                   # Composition Root: wires Repository→Service→Handler, starts the HTTP server
│   │   ├── health.go                 # GET /health (liveness) and GET /health/ready (readiness) handlers
│   │   ├── main_test.go              # Server lifecycle tests (Shutdown behavior, health/readiness endpoints)
│   │   └── main_integration_test.go  # Full-stack HTTP test: real Handler+Service+memoryRepository
│   └── migrate/
│       └── main.go                   # Standalone CLI: applies/reverts PostgreSQL migrations (make migrate-up / migrate-down)
├── config/
│   ├── config.go          # Environment variable loading and validation — the only package that reads os.Getenv
│   ├── config_test.go
│   ├── dotenv.go          # Minimal .env file loader
│   └── dotenv_test.go
├── middleware/
│   ├── middleware.go      # Chain() composes middleware
│   ├── request_id.go      # Generates/propagates X-Request-ID
│   ├── logging.go         # Structured per-request access log line
│   ├── recovery.go        # Panic recovery → HTTP 500
│   └── *_test.go
├── task/
│   ├── task.go                       # Domain model: Task struct, Status type
│   ├── errors.go                     # Domain error sentinels (ErrNotFound, ErrInvalidInput, ErrAlreadyExists, ErrConflict)
│   ├── repository.go                 # Repository interface — the boundary Service depends on
│   ├── memory_repository.go          # In-memory Repository implementation (sync.RWMutex, CAS-based optimistic concurrency)
│   ├── memory_repository_test.go     # Unit tests
│   ├── postgres_repository.go        # PostgreSQL Repository implementation (*sql.DB, transactions, parameterized queries)
│   ├── postgres_repository_test.go   # Integration tests — build-tagged `integration`, need a real PostgreSQL (see Testing)
│   ├── postgres_migrate.go           # Embedded migration runner (RunMigrations / RunMigrationsDown)
│   ├── migrations/
│   │   ├── 0001_create_tasks_table.up.sql
│   │   └── 0001_create_tasks_table.down.sql
│   ├── service.go                    # Business logic: validation, ID generation, timestamps
│   ├── service_test.go               # Unit tests (fake Repository)
│   ├── handler.go                    # HTTP handlers, route registration, pagination param parsing
│   ├── handler_test.go               # Unit tests (fake Service)
│   └── integration_test.go           # Full-stack HTTP tests using the *real* memoryRepository (no external dependency — see Testing)
├── docker-compose.yml      # Local stack: PostgreSQL, and optionally the API itself
├── Dockerfile              # Multi-stage build → static binary on `scratch`
├── Makefile                # make run / test / test-race / coverage / lint / docker-* / migrate-*
├── .env.example
├── .github/workflows/ci.yml
└── go.mod / go.sum
```

## Requirements

- **Go 1.26** or later, matching the version pinned in `go.mod` (the codebase and its tests use `http.ServeMux` path parameters, `log/slog`, range-over-int, and `sync.WaitGroup.Go`).
- **No external dependency for the core application or the in-memory store.** `task`, `config`, and `middleware` — plus the entire unit test suite — are standard library only. The single external module dependency in `go.mod` is [`github.com/jackc/pgx/v5`](https://github.com/jackc/pgx), used exclusively to talk to PostgreSQL when `DATABASE_URL` is configured (see [Design Decisions](#design-decisions)).
- **[Docker](https://www.docker.com/) and Docker Compose** (optional, but needed for [Running with Docker](#running-with-docker) and the PostgreSQL integration tests) — to run PostgreSQL locally without installing it directly.
- **[`staticcheck`](https://staticcheck.dev/)** (optional) — used by `make lint`; installed automatically into `$GOBIN` on first use if missing.

## Configuration

All configuration is read from environment variables by `config.Load()` — it is the *only* place in the codebase that calls `os.Getenv`; every other package receives typed configuration values, never touching the environment itself. If a variable is not set, a documented default is used, so the application runs with zero configuration out of the box (in-memory store, `:8080`).

On startup, `config.Load()` reads a `.env` file from the working directory (if present) and applies any variable defined there that isn't already set in the real OS environment — real environment variables always win over `.env`. That makes `.env` safe for local defaults while still letting deployment environments (CI, containers) override anything explicitly.

```bash
cp .env.example .env   # then edit .env for your local setup
```

`.env` is listed in `.gitignore` and must never be committed — `.env.example` is the only env file tracked in version control, and it must only ever contain non-sensitive placeholder values.

| Variable | Description | Default | Example |
|---|---|---|---|
| `HTTP_ADDR` | TCP address the HTTP server listens on (`host:port`) | `:8080` | `HTTP_ADDR=:9090` |
| `HTTP_READ_TIMEOUT` | `http.Server.ReadTimeout` | `5s` | `HTTP_READ_TIMEOUT=10s` |
| `HTTP_WRITE_TIMEOUT` | `http.Server.WriteTimeout` | `10s` | `HTTP_WRITE_TIMEOUT=15s` |
| `HTTP_IDLE_TIMEOUT` | `http.Server.IdleTimeout` | `60s` | `HTTP_IDLE_TIMEOUT=120s` |
| `HTTP_SHUTDOWN_TIMEOUT` | Maximum time to wait for in-flight requests during shutdown | `10s` | `HTTP_SHUTDOWN_TIMEOUT=30s` |
| `LOG_LEVEL` | Minimum log level: `debug`, `info`, `warn`, or `error` | `info` | `LOG_LEVEL=debug` |
| `DOTENV_PATH` | Path to the `.env` file `Load` reads before the OS environment | `.env` | `DOTENV_PATH=/etc/task-api/.env` |
| `DATABASE_URL` | PostgreSQL connection string. Unset means "use the in-memory store" | *(unset)* | `postgres://user:pass@host:5432/dbname?sslmode=disable` |
| `DB_MAX_OPEN_CONNS` | Maximum open connections in the pool (`sql.DB.SetMaxOpenConns`) | `25` | `DB_MAX_OPEN_CONNS=50` |
| `DB_MAX_IDLE_CONNS` | Maximum idle connections kept in the pool (`sql.DB.SetMaxIdleConns`) | `25` | `DB_MAX_IDLE_CONNS=10` |
| `DB_CONN_MAX_LIFETIME` | Maximum lifetime of a pooled connection (`sql.DB.SetConnMaxLifetime`) | `5m` | `DB_CONN_MAX_LIFETIME=15m` |
| `DB_AUTO_MIGRATE` | Apply pending PostgreSQL migrations automatically on startup | `true` | `DB_AUTO_MIGRATE=false` |

**Validation rules** (`config.Load` returns an error and the process refuses to start if any of these fail):

- `HTTP_ADDR` must be a syntactically valid `host:port` address (per `net.SplitHostPort`) with a numeric port between 1 and 65535. Port `0` is rejected.
- All `HTTP_*_TIMEOUT` and `DB_CONN_MAX_LIFETIME` variables must be a valid Go duration string (`10s`, `1m`, `500ms`, ...) and strictly positive.
- `LOG_LEVEL` is case-insensitive and must be one of `debug`, `info`, `warn`/`warning`, or `error`.
- `DB_MAX_OPEN_CONNS` and `DB_MAX_IDLE_CONNS` must be positive integers.
- `DB_AUTO_MIGRATE` must be a valid boolean (`true`/`false`/`1`/`0`/...).
- `DATABASE_URL` is **not** format-validated by `config.Load` — connection string formats vary, and the PostgreSQL driver is the authority on what it accepts. An invalid value surfaces at startup with the driver's own error, when the application opens and pings the connection.

## Running Locally

```bash
git clone https://github.com/JonasBorgesLM/task-api.git
cd task-api
cp .env.example .env      # optional — sensible defaults apply without it
go run ./cmd/api           # or: make run
```

```
{"time":"...","level":"INFO","msg":"server started","addr":":8080"}
```

With no `DATABASE_URL` set, this uses the in-memory store — data does not survive a restart, and no external service is needed. To run against real PostgreSQL instead, see [Migrations](#migrations) and [Running with Docker](#running-with-docker) below (or point `DATABASE_URL` at any reachable PostgreSQL instance, containerized or not).

Override any variable for a single run without editing `.env`:

```bash
HTTP_ADDR=:9090 HTTP_SHUTDOWN_TIMEOUT=30s go run ./cmd/api
```

Build a standalone binary:

```bash
go build -o bin/task-api ./cmd/api   # or: make build
./bin/task-api
```

## Running with Docker

The `Dockerfile` is a two-stage build: `golang:1.26.5-alpine` compiles a static (`CGO_ENABLED=0`), stripped binary, which is then copied into an empty `scratch` image — no shell, no package manager, no libc. The final image is just the binary plus a nonroot user.

**Build the image:**

```bash
docker build -t task-api:latest .   # or: make docker-build
```

**Run the full stack (API + PostgreSQL) via Docker Compose:**

```bash
docker compose up -d --build   # or: make docker-up
curl http://localhost:8080/health
docker compose down             # or: make docker-down
```

`docker-compose.yml` defines two services: `postgres` (with a health check, so `api` waits for it to actually be ready to accept connections, not just for its process to start) and `api` (built from the `Dockerfile`, `DATABASE_URL` pointed at `postgres` over the Compose network, migrations applied automatically on startup).

**Run only PostgreSQL** (for faster edit/rebuild cycles: run the API on the host with `go run ./cmd/api` / `make run` instead of rebuilding a container image on every change):

```bash
docker compose up -d postgres   # or: make db-up
DATABASE_URL="postgres://task_api:task_api@localhost:5432/task_api?sslmode=disable" go run ./cmd/api
```

**Run the built image standalone**, against any PostgreSQL instance:

```bash
docker run --rm -p 8080:8080 \
  -e DATABASE_URL="postgres://user:pass@host:5432/dbname?sslmode=disable" \
  task-api:latest
```

## Testing

The suite is split into two kinds of tests that are never mixed in the same file or run:

| | Unit tests | Integration tests |
|---|---|---|
| **What they exercise** | `Service` and `Handler` against fakes; `memoryRepository`; config/middleware in isolation; a full HTTP stack wired against the real `memoryRepository` (`task/integration_test.go` — real wiring, but still zero external dependencies) | `postgresRepository` against a **real PostgreSQL instance** |
| **External dependency** | None | PostgreSQL (`TEST_DATABASE_URL`) |
| **How they're separated** | Default build | `//go:build integration` tag on `task/postgres_repository_test.go` — this file is not even compiled by a plain `go test ./...` |
| **Run with** | `go test ./...` / `make test` | `go test -tags=integration ./task/... -run Postgres` / `make test-integration` |
| **Speed** | Milliseconds; safe to run on every save | Needs a live database; run before pushing / in CI |

`task/integration_test.go` is a full-stack HTTP test (real `Handler` → real `Service` → real `memoryRepository`, driven through `httptest.Server`), and its name is a potential source of confusion: it is *not* one of the PostgreSQL integration tests. It has no external dependency and is part of the unit suite, because it validates that the layers are wired together correctly — not that PostgreSQL works. The dividing line is "needs a real external service," not "spans more than one layer."

```bash
go test ./...        # or: make test — unit tests only, no external service needed
```

Running the PostgreSQL integration tests requires a real database (see [Running with Docker](#running-with-docker)):

```bash
make db-up              # start PostgreSQL via docker compose
make test-integration   # go test -tags=integration ./task/... -run Postgres -v
```

`task/postgres_repository_test.go` covers, against a real PostgreSQL instance: `Create` (including a duplicate-ID conflict and a SQL-injection attempt), `FindByID` (including not-found and a malformed, non-UUID ID), `FindAll` (ordering, `LIMIT`/`OFFSET` pagination, and the empty case), `Update` (including not-found, a version-mismatch conflict, and real concurrent goroutines racing on the same row), `Delete` (including not-found), context cancellation, and the database-level `CHECK` constraint on `status`. `newPostgresTestRepo` also `TRUNCATE`s the `tasks` table before every test, so each test starts from a clean, isolated state despite sharing one physical database — and it skips (not fails) if `TEST_DATABASE_URL` isn't set, so an accidental `-tags=integration` run on a machine with no PostgreSQL degrades gracefully.

## Race Detector

```bash
go test -race ./...             # or: make test-race — unit tests
go test -race -tags=integration ./task/... -run Postgres   # integration tests
```

The race detector is part of the CI quality gate for both suites. `TestConcurrentUpdate_LosersGetErrConflict` (unit, `memoryRepository`) and `TestPostgres_ConcurrentUpdate_LosersGetErrConflict` (integration, real PostgreSQL) both drive real concurrent goroutines through the optimistic-concurrency path specifically so `-race` has real cross-goroutine memory access to check, not just sequential calls.

## Coverage

```bash
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out   # or both, via: make coverage
```

This covers the unit suite only — `postgresRepository` and the migration runner intentionally show `0.0%` here, since exercising them needs the real PostgreSQL integration run (`make test-integration`) instead of a coverage-instrumented one. For a visual, file-by-file view: `go tool cover -html=coverage.out`.

## Migrations

The `tasks` table's schema lives in `task/migrations/0001_create_tasks_table.{up,down}.sql`, embedded into the binary via `embed.FS` (`task/postgres_migrate.go`) — the compiled binary carries its own schema and needs no migration files deployed alongside it. Applied migrations are tracked in a `schema_migrations` table; each migration is applied (or reverted) inside its own transaction, so a failure partway through never leaves the schema half-migrated.

**Automatic, on API startup** — the default (`DB_AUTO_MIGRATE=true`): whenever `DATABASE_URL` is set, `cmd/api/main.go` applies every pending migration before serving any request.

**Manual, via the standalone `cmd/migrate` CLI** — for reverting a migration (the server only ever migrates forward), or for deployments that set `DB_AUTO_MIGRATE=false` and want migrations run once, explicitly, as their own deploy step (recommended once you're running more than one API replica — every instance auto-migrating concurrently on boot is best avoided):

```bash
make db-up   # if not already running

make migrate-up     # go run ./cmd/migrate -direction=up   — apply pending migrations
make migrate-down    # go run ./cmd/migrate -direction=down — revert the last applied migration
```

Both are idempotent-safe to re-run: `migrate-up` with nothing pending prints `migrations applied` and changes nothing; `migrate-down` with nothing to revert prints `no migrations to revert` and exits cleanly (`task.ErrNoMigrationsToRevert`).

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

| Code | Reason |
|---|---|
| `201` | Task created successfully |
| `400` | Missing or empty title, or malformed JSON |
| `409` | A task with the generated ID already exists (practically unreachable — IDs are server-generated UUIDv4) |
| `500` | Unexpected server error |

---

### `GET /tasks`

Returns tasks ordered by `created_at` ascending (oldest first, ties broken by `id`). Returns an empty array when no tasks exist.

**Query parameters** (both optional; omitting both returns everything)

| Parameter | Type | Description |
|---|---|---|
| `limit` | integer ≥ 0 | Maximum number of tasks to return. |
| `offset` | integer ≥ 0 | Number of tasks to skip before collecting `limit` results. |

| Code | Reason |
|---|---|
| `200` | Success (empty array `[]` if no tasks exist, or if `offset` is past the end of the list) |
| `400` | `limit` or `offset` is present but not a non-negative integer |
| `500` | Unexpected server error |

---

### `GET /tasks/{id}`

Returns a single task by ID.

| Code | Reason |
|---|---|
| `200` | Task found |
| `404` | No task with the given ID |
| `500` | Unexpected server error |

---

### `PUT /tasks/{id}`

Updates the title and description of an existing task. `CreatedAt`, `Status`, and `ID` are not affected.

**Request body** — same shape and constraints as `POST /tasks`.

| Code | Reason |
|---|---|
| `200` | Task updated |
| `400` | Missing/empty/overlong title, overlong description, or malformed JSON |
| `404` | No task with the given ID |
| `409` | The task was modified by another request between your read and this write (optimistic concurrency conflict) — re-fetch and retry |
| `500` | Unexpected server error |

---

### `PATCH /tasks/{id}/done`

Marks a task as done. **Idempotent**: calling it on an already-`done` task returns the task unchanged, without modifying `UpdatedAt`.

No request body required.

| Code | Reason |
|---|---|
| `200` | Task marked as done (or was already done) |
| `404` | No task with the given ID |
| `409` | Optimistic concurrency conflict — re-fetch and retry. Never returned for a task that was already `done`, since that path never writes. |
| `500` | Unexpected server error |

---

### `DELETE /tasks/{id}`

Removes a task permanently. No request body. No response body.

| Code | Reason |
|---|---|
| `204` | Task deleted |
| `404` | No task with the given ID |
| `500` | Unexpected server error |

---

### `GET /health`

Liveness check: always responds `200 OK` while the process is running: `{"status": "ok"}`. It does not check PostgreSQL or any other dependency — that's what `GET /health/ready` is for.

### `GET /health/ready`

Readiness check: responds `200 OK` with `{"status": "ok"}` if the backing store is reachable, or `503 Service Unavailable` with `{"status": "unavailable"}` if it isn't. For the in-memory store (no `DATABASE_URL`) it always reports ready — there's nothing external to check. For PostgreSQL, it calls `db.PingContext` (bounded by a 5s timeout) through the `task.Pinger` interface `postgresRepository` implements.

Use this, not `GET /health`, for a Kubernetes readiness probe or load balancer health check: it's what lets an orchestrator stop routing traffic to a replica whose database connection is broken, instead of only detecting a hard process crash.

### `GET /debug/vars`

Standard library `expvar` endpoint — see [Observability](#observability).

## Example Requests

A full lifecycle against a running server (`go run ./cmd/api`, default `http://localhost:8080`):

```bash
# Create
curl -s -X POST http://localhost:8080/tasks \
  -H 'Content-Type: application/json' \
  -d '{"title":"Buy groceries","description":"Milk, eggs, bread"}'
# → 201 {"id":"f47ac10b-...","title":"Buy groceries", ..., "status":"pending", ...}

ID=f47ac10b-58cc-4372-a567-0e02b2c3d479   # substitute the id from the response above

# Read
curl -s http://localhost:8080/tasks/$ID
# → 200 {"id":"f47ac10b-...", ...}

# List, paginated
curl -s "http://localhost:8080/tasks?limit=10&offset=0"
# → 200 [ {...}, ... ]

# Update
curl -s -X PUT http://localhost:8080/tasks/$ID \
  -H 'Content-Type: application/json' \
  -d '{"title":"Buy groceries and cook dinner","description":"Don'"'"'t forget olive oil"}'
# → 200 {"id":"f47ac10b-...","title":"Buy groceries and cook dinner", ...}

# Mark complete (idempotent — safe to call twice)
curl -s -X PATCH http://localhost:8080/tasks/$ID/done
# → 200 {"id":"f47ac10b-...","status":"done", ...}

# Delete
curl -s -o /dev/null -w '%{http_code}\n' -X DELETE http://localhost:8080/tasks/$ID
# → 204

# Now gone
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/tasks/$ID
# → 404
```

Error examples:

```bash
# Empty title → 400
curl -s -X POST http://localhost:8080/tasks -H 'Content-Type: application/json' -d '{"title":""}'
# → 400 {"error":"invalid input: title must not be empty"}

# Unknown id → 404
curl -s http://localhost:8080/tasks/does-not-exist
# → 404 {"error":"task not found"}
```

## Error Handling

All error responses use the same JSON envelope:

```json
{ "error": "description of the problem" }
```

| HTTP Code | Meaning |
|---|---|
| `400 Bad Request` | Invalid input — malformed JSON, failed business rule validation (e.g. empty or overlong title), or an invalid `limit`/`offset`. The message describes the specific problem. |
| `404 Not Found` | The requested task ID does not exist. |
| `409 Conflict` | A task with the generated ID already exists on `POST /tasks` (practically unreachable, since IDs are server-generated UUIDv4), or `PUT`/`PATCH .../done` lost an optimistic concurrency race against a concurrent write — re-fetch and retry. |
| `500 Internal Server Error` | An unexpected server-side failure. The response body carries only a generic message; details are logged server-side (see [Observability](#observability)) and never exposed to the client. |

`Handler.handleServiceError` is the single place this mapping happens: it uses `errors.Is` against the domain sentinels in `task/errors.go` (`ErrNotFound`, `ErrInvalidInput`, `ErrAlreadyExists`, `ErrConflict`), which both `memoryRepository` and `postgresRepository` return identically — `Handler` never has a PostgreSQL-specific branch.

## Graceful Shutdown

The server handles `SIGINT` (Ctrl+C) and `SIGTERM` (sent by process managers and container runtimes):

1. The signal is received via `signal.NotifyContext`, canceling a `context.Context` that `run()` in `cmd/api/main.go` is watching.
2. `http.Server.Shutdown(ctx)` is called with a fresh context bounded by `HTTP_SHUTDOWN_TIMEOUT`.
3. The server stops accepting new connections immediately.
4. In-flight requests are allowed to complete.
5. Once the HTTP server has fully stopped, the `Repository`'s resources are released — for `postgresRepository`, this closes the `*sql.DB` connection pool. This happens *after* `Shutdown` returns, specifically so a request still in flight is never cut off from its database connection mid-shutdown.
6. If the timeout expires before all requests finish, `Shutdown` returns an error, which is logged and propagates to a non-zero process exit code.
7. A clean shutdown logs `shutdown completed` and the process exits `0`.

## Observability

- **Structured logging (`log/slog`, JSON)** — every log line is a JSON object; level is controlled by `LOG_LEVEL`. The logger is constructed once in `cmd/api/main.go` and injected into `Handler`; there is no global mutable logger.
- **Per-request access log** — `middleware.Logging` logs one line per request: method, path, status code, duration, and the request ID (see below). This is the single source of truth for "what happened to this request" — `Handler` does not separately log routine outcomes like `404`/`400`/`409`, since the access log line already records the result.
- **Request correlation (`X-Request-ID`)** — `middleware.RequestID` generates (or propagates, if the client already sent one) a request ID, attaches it to the request context, and echoes it in the `X-Request-ID` response header. Every log line for a request — the access log line, and any `Error`-level line logged for an unexpected failure — carries the same ID, so a report of "request X failed" can be grepped straight to its server-side log lines.
- **Panic recovery** — `middleware.Recovery` catches a panic in any handler, logs it (with the request ID) at `Error` level, and returns `500` instead of crashing the process or leaking a stack trace to the client.
- **Unexpected-error logging** — `Handler.handleServiceError` logs only the errors it doesn't have a specific HTTP mapping for (i.e. genuine `500`s), once, at `Error` level, with the request ID, method, and path attached. Expected outcomes (`404`, `400`, `409`) are not logged a second time beyond the access log line.
- **`GET /debug/vars`** — the standard library `expvar` endpoint: command line, memory stats, GC stats, goroutine-related counters, as JSON. Baseline runtime visibility with no external dependency; see [Future Improvements](#future-improvements) for where a richer metrics surface would go.

## Design Decisions

**Service contains all business rules.** Validation (non-empty title, length limits, measured in Unicode characters via `utf8.RuneCountInString` — not bytes, which would misjudge non-ASCII text), ID generation, and timestamp management live exclusively in `Service`. `Handler` translates HTTP; `Repository` stores data. Neither of those two layers contains domain logic. (Result ordering and pagination windowing are `Repository`'s responsibility — see below — precisely so a SQL-backed `Repository` can push them into the query instead of `Service` re-deriving them from an already-fetched, unbounded result set.)

**`Repository` is interface-based, with two implementations.** `cmd/api/main.go`'s `buildRepository` picks the concrete implementation at startup — `memoryRepository` or `postgresRepository`, based solely on whether `DATABASE_URL` is set — and injects it into `Service`. Adding `postgresRepository` required zero changes to `Service` or `Handler`. `memoryRepository` is not a placeholder scheduled for removal: it stays because it needs no external service, keeping the unit test suite fast and hermetic.

**UUID generated in the Service.** `Service.newID` generates a UUIDv4 using `crypto/rand`. `Repository` receives an already fully-formed `Task` and never assigns IDs — including in `postgresRepository`, where the `id` column has no `gen_random_uuid()` default. Storage never invents a business identifier.

**Timestamps managed by the Service.** `CreatedAt` is set once at creation and never modified; `UpdatedAt` is refreshed on every mutation. The HTTP request structs don't even include these fields, so a client cannot set or override them.

**`CompleteTask` is idempotent.** Calling `PATCH /tasks/{id}/done` on an already-`done` task returns the current task unchanged, without touching `UpdatedAt` or calling `Repository.Update` at all — so it can never `409` on an already-complete task.

**Optimistic concurrency control on updates.** `Task` carries an internal `Version` field (`json:"-"`, never on the wire). `UpdateTask`/`CompleteTask` read a task, then write it back passing along the `Version` they read. Both `Repository` implementations reject the write with `ErrConflict` (HTTP `409`) if the stored `Version` has since changed — i.e. another writer got there first — instead of silently overwriting it (a "lost update"). `Create` always resets `Version` to `1`; a successful `Update` increments it. `memoryRepository` does this with a compare-and-swap under its mutex; `postgresRepository` does it with `SELECT ... FOR UPDATE` inside a transaction (see below) — both are exercised by dedicated tests that drive real concurrent goroutines through the race (`TestConcurrentUpdate_LosersGetErrConflict`, `TestPostgres_ConcurrentUpdate_LosersGetErrConflict`).

**`context.Context` propagates end-to-end.** Every `Repository` and `Service` method takes `ctx context.Context`, sourced from `r.Context()` in `Handler`. `postgresRepository` threads the same `ctx` down to every `*sql.DB`/`*sql.Tx` call (`QueryRowContext`, `ExecContext`, `BeginTx`, ...), so a canceled request or expired deadline aborts the in-flight PostgreSQL query instead of running it to completion for a client that's already gone.

**Domain errors are translated once, at the Handler layer** — see [Error Handling](#error-handling).

**Request bodies are capped at 1 MiB.** `POST /tasks` and `PUT /tasks/{id}` wrap the body in `http.MaxBytesReader` before decoding, so an oversized payload is rejected instead of being fully buffered into memory.

**`GET /tasks` ordering and pagination are enforced by `Repository`, not `Service`.** `Repository.FindAll(ctx, limit, offset)` guarantees its result is ordered by `CreatedAt` ascending (ties broken by `ID`) and windowed to `limit`/`offset` — `Service.ListTasks` just passes those two integers through and returns whatever comes back, without re-sorting or re-slicing. `memoryRepository.FindAll` sorts the snapshot it takes of its map (which has no iteration order of its own) and then slices it. `postgresRepository.FindAll` pushes both concerns into the query itself: `ORDER BY created_at, id LIMIT $1 OFFSET $2` (`LIMIT NULL` when `limit < 0`, PostgreSQL's own "no limit" spelling) — so a page of results costs roughly `offset + limit` rows read, not the whole table. This is a deliberate change from fetching every row and paginating client-side in `Handler`: with an unbounded `Repository.FindAll(ctx)` (no window), `GET /tasks?limit=10` against a large table would still scan and transfer every row on every call, and only discard most of them after the fact — the window has to be inside the query to actually bound the work done per request. `idx_tasks_created_at_id` (below) is what makes the `ORDER BY` in that query cheap as the table grows.

### PostgreSQL implementation

**Library choice: `database/sql` + `pgx/v5`'s `stdlib` driver**, not `pgx`'s native pool. Reasoning:

- `lib/pq` is in maintenance mode (its own README says so and recommends `pgx`); `pgx` is the actively maintained, de facto standard Go PostgreSQL driver today.
- `pgx`'s native interface (`pgxpool.Pool`) is arguably more idiomatic *pgx*, but it isn't `*sql.DB`. This project's pooling/lifecycle needs (`SetMaxOpenConns`/`SetMaxIdleConns`/`SetConnMaxLifetime`, `Ping`, `Close`) are fully met by the standard library's own primitives, so `postgresRepository` is written against a standard, widely understood interface (`*sql.DB`, `*sql.Tx`, `context`-aware methods) while `pgx` still does the wire-protocol work underneath.
- `pgx` is pure Go — no `libpq` C library, no cgo. That's what lets the production `Dockerfile` build a static (`CGO_ENABLED=0`) binary into a `scratch` image with no libc at all.
- Its own dependency tree is small (`pgpassfile`, `pgservicefile`, `golang.org/x/text`) and entirely from the same maintainers or the Go team — no sprawling transitive graph.

**Migrations use a small embedded runner, not a dedicated tool.** `task/postgres_migrate.go` embeds `task/migrations/*.sql` via `embed.FS`, tracks applied versions in `schema_migrations`, and applies/reverts each migration inside its own transaction. Adding a dependency like `golang-migrate` for a single table wasn't justified; the `.up.sql`/`.down.sql` naming still follows that ecosystem's convention, so the same files would work unmodified if the project ever outgrows this.

**Schema** (`task/migrations/0001_create_tasks_table.up.sql`):

| Column | Type | Why |
|---|---|---|
| `id` | `UUID PRIMARY KEY` | No server-side default — `Service.newID` generates it application-side; `Repository` never invents identifiers. |
| `title` | `VARCHAR(200) NOT NULL` | Mirrors `task.maxTitleLen` as a structural backstop; `Service` is still what enforces this on the way in. |
| `description` | `VARCHAR(2000) NOT NULL DEFAULT ''` | Mirrors `task.maxDescriptionLen`, same reasoning. |
| `status` | `VARCHAR(20) NOT NULL DEFAULT 'pending'`, `CHECK (status IN ('pending','done'))` | Keeps `Status` a genuinely controlled state at the storage layer. The `CHECK` fences which *values* are valid; it does not encode which *transitions* are legal — that stays entirely in `Service`. |
| `created_at`, `updated_at` | `TIMESTAMPTZ NOT NULL` | Stored unambiguously in UTC regardless of server timezone. `Service` sets both on every write. |
| `version` | `INTEGER NOT NULL DEFAULT 1` | Backs the optimistic-concurrency contract described above. Never on the wire. |

**Indexes:** the `id` primary key covers `FindByID`/`Update`/`Delete`. The one additional index, `idx_tasks_created_at_id` on `(created_at, id)`, supports `FindAll`'s `ORDER BY` without a sort step as the table grows — deliberately the *only* extra index, since no other query shape (e.g. filtering by `status`) exists yet to justify one's write-amplification and storage cost. A `status` index is the natural addition if/when `GET /tasks` grows a status filter.

**Transactions.** `Update`'s optimistic-concurrency contract needs more than one statement: read the current `Version`, compare, then write. Run as separate round-trips, two concurrent `Update`s could both read the same `Version`, both pass the comparison, and the second write would silently clobber the first. `postgresRepository.Update` closes that race with `SELECT version FROM tasks WHERE id = $1::uuid FOR UPDATE` inside a transaction — the row lock makes a second concurrent transaction's own `SELECT ... FOR UPDATE` block until the first commits or rolls back, so it observes the already-incremented `Version` and correctly returns `ErrConflict`. Migrations use the same atomicity pattern in reverse (DDL + `schema_migrations` bookkeeping in one transaction), so a crash mid-migration can never leave the schema changed-but-unrecorded or recorded-but-not-applied.

**Error handling.** `postgresRepository` translates PostgreSQL-specific failures into the *same* sentinel errors `memoryRepository` returns: `sql.ErrNoRows` → `ErrNotFound`; a unique-violation on `Create` (SQLSTATE `23505`, via `errors.As` into `*pgconn.PgError`) → `ErrAlreadyExists`; a `Version` mismatch → `ErrConflict`. Anything else is wrapped (`fmt.Errorf("postgres: ...: %w", err)`) and surfaces as a generic `500` — `Handler` never branches on PostgreSQL specifically.

**SQL injection.** Every query uses `database/sql` placeholders (`$1`, `$2`, ...); no request-derived value is ever concatenated into SQL text. `id` values are additionally cast on the Postgres side (`$1::uuid`), so a malformed ID is rejected by the type system rather than mismatching at the wire level. `TestPostgres_Create_RejectsSQLInjectionAttempt` creates a task whose title is a `DROP TABLE` attempt and asserts it's stored as inert text with the table intact.

## Future Improvements

- **Authentication and authorization** — protect endpoints with JWT or API key middleware.
- **Application-level metrics** — `GET /debug/vars` gives baseline Go runtime stats; a richer surface (request count/latency/error-rate per route) needs dedicated instrumentation, e.g. Prometheus.
- **Distributed tracing** — OpenTelemetry, for end-to-end request tracing.
- **Task filtering** — filter tasks by status on `GET /tasks` (the natural point to also add the `status` index discussed above).
- **`golangci-lint` in CI** — `make lint` currently uses `staticcheck` because the `golangci-lint` build available in this environment predates the Go version pinned in `go.mod` and cannot type-check it; once a compatible build is available, it's a straightforward addition alongside (or instead of) `staticcheck`.
