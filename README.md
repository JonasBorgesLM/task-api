# Task API

A small, production-shaped HTTP REST API for task management, written in Go — a compact reference for how a real Go service is put together: layered architecture, a swappable persistence layer (in-memory or PostgreSQL, behind one interface), structured logging, graceful shutdown, health/readiness checks, and a test suite that keeps unit and integration tests genuinely separate.

## Table of Contents

1. [Overview](#overview)
2. [Architecture](#architecture)
3. [Project Structure](#project-structure)
4. [Requirements](#requirements)
5. [Configuration](#configuration)
6. [Running Locally](#running-locally)
7. [Running with Docker](#running-with-docker)
8. [Testing](#testing)
9. [Migrations](#migrations)
10. [API](#api)
11. [Graceful Shutdown](#graceful-shutdown)
12. [Observability](#observability)
13. [Design Decisions](#design-decisions)
14. [Future Improvements](#future-improvements)

## Overview

Task API manages the lifecycle of a task: a title, an optional description, and a status (`pending` or `done`) — create, read (single and list, with pagination), update, mark-complete, delete.

The functional surface is intentionally small. The point of the project is what's *underneath* it: a layered architecture where each layer depends only on an interface, never a concrete implementation; a persistence layer swappable between an in-memory store and PostgreSQL without the business logic or HTTP layer knowing which one is in use; explicit configuration and dependency wiring with no hidden globals; and a development workflow (Docker, migrations, CI, tests) that mirrors how this would actually be operated.

For AI agents (or new contributors) working in this codebase, see **[CLAUDE.md](CLAUDE.md)** — architecture rules, conventions, and things not to change casually.

## Architecture

Three layers, each depending only on the layer below it, and only through an interface:

```
HTTP Request
     │
     ▼
┌─────────────┐
│   Handler   │  Decodes HTTP requests, calls Service, encodes responses
└──────┬──────┘
       ▼
┌─────────────┐
│   Service   │  Validates input, applies business rules, manages timestamps and IDs
└──────┬──────┘
       ▼
┌─────────────┐
│ Repository  │  Persists and retrieves tasks — interface only
└──────┬──────┘
       │ implemented by
       ├──────────────────────┐
       ▼                      ▼
┌────────────────┐   ┌─────────────────────┐
│ memoryRepository│   │  postgresRepository │
│ (in-process,    │   │  (*sql.DB, real     │
│  no external    │   │   PostgreSQL)       │
│  dependency)    │   │                     │
└────────────────┘   └─────────────────────┘
```

`cmd/api/main.go` is the **Composition Root** — the only place that instantiates concrete types and wires them together. `Service` and `Handler` depend only on the `Repository` interface (`task/repository.go`) and are completely unaware PostgreSQL exists; only `cmd/api/main.go` and `cmd/migrate/main.go` import a PostgreSQL package. Swapping `memoryRepository` for `postgresRepository` — or adding a third implementation — requires zero changes to business logic or HTTP handling.

## Project Structure

```
task-api/
├── cmd/
│   ├── api/
│   │   ├── main.go                   # Composition Root: wires Repository→Service→Handler, starts the HTTP server
│   │   ├── health.go                 # GET /health (liveness) and GET /health/ready (readiness)
│   │   └── *_test.go                 # Server lifecycle + full-stack HTTP tests
│   └── migrate/
│       └── main.go                   # Standalone CLI: applies/reverts PostgreSQL migrations
├── config/                # Environment variable loading and validation (the only package that reads os.Getenv)
├── middleware/             # RequestID, Logging, Recovery — composed via Chain()
├── task/
│   ├── task.go                       # Domain model: Task struct, Status type
│   ├── errors.go                     # Domain error sentinels (ErrNotFound, ErrInvalidInput, ErrAlreadyExists, ErrConflict)
│   ├── repository.go                 # Repository interface — the boundary Service depends on
│   ├── memory_repository.go          # In-memory implementation (sync.RWMutex, CAS-based optimistic concurrency)
│   ├── postgres_repository.go        # PostgreSQL implementation (*sql.DB, transactions, parameterized queries)
│   ├── postgres_repository_test.go   # Integration tests — build-tagged `integration`, need real PostgreSQL
│   ├── postgres_migrate.go           # Embedded migration runner (RunMigrations / RunMigrationsDown)
│   ├── migrations/                   # *.up.sql / *.down.sql, embedded into the binary
│   ├── service.go                    # Business logic: validation, ID generation, timestamps
│   ├── handler.go                    # HTTP handlers, route registration, pagination param parsing
│   └── integration_test.go           # Full-stack HTTP tests using the *real* memoryRepository (no external dependency)
├── docs/openapi.yaml       # Full API contract — every endpoint, schema, status code, example
├── docker-compose.yml      # Local stack: PostgreSQL, and optionally the API itself
├── Dockerfile              # Multi-stage build → static binary on `scratch`
├── Makefile                # `make help` for the full command list
├── CLAUDE.md               # Guidance for AI agents / contributors working in this repo
└── .env.example
```

## Requirements

- **Go 1.26+**, matching `go.mod`.
- **No external dependency for the core application or the in-memory store** — `task`, `config`, `middleware`, and the entire unit test suite are standard library only. The single runtime dependency is [`pgx/v5`](https://github.com/jackc/pgx), used only when `DATABASE_URL` is configured.
- **[Docker](https://www.docker.com/) and Docker Compose** (optional) — to run PostgreSQL locally without installing it directly.
- **[`staticcheck`](https://staticcheck.dev/)** (optional) — used by `make lint`; installed automatically on first use if missing.

## Configuration

Every setting is an environment variable, read once by `config.Load()` — the only place in the codebase that calls `os.Getenv`. Unset variables fall back to the defaults below, so the app runs with zero configuration (in-memory store, `:8080`).

```bash
cp .env.example .env   # optional — edit for your local setup; real env vars always win over .env
```

`.env` is gitignored and must never be committed; `.env.example` (placeholder values only) is the tracked template.

| Variable | Description | Default |
|---|---|---|
| `HTTP_ADDR` | TCP address the HTTP server listens on (`host:port`) | `:8080` |
| `HTTP_READ_TIMEOUT` | `http.Server.ReadTimeout` | `5s` |
| `HTTP_WRITE_TIMEOUT` | `http.Server.WriteTimeout` | `10s` |
| `HTTP_IDLE_TIMEOUT` | `http.Server.IdleTimeout` | `60s` |
| `HTTP_SHUTDOWN_TIMEOUT` | Max time to wait for in-flight requests during shutdown | `10s` |
| `LOG_LEVEL` | `debug`, `info`, `warn`, or `error` | `info` |
| `DOTENV_PATH` | Path to the `.env` file loaded before the OS environment | `.env` |
| `DATABASE_URL` | PostgreSQL connection string. Unset ⇒ in-memory store | *(unset)* |
| `DB_MAX_OPEN_CONNS` | Max open connections in the pool | `25` |
| `DB_MAX_IDLE_CONNS` | Max idle connections kept in the pool | `25` |
| `DB_CONN_MAX_LIFETIME` | Max lifetime of a pooled connection | `5m` |
| `DB_AUTO_MIGRATE` | Apply pending migrations automatically on startup | `true` |

`config.Load()` returns an error (and the process refuses to start) if a timeout isn't a positive Go duration, `HTTP_ADDR` isn't a valid `host:port` with a port in 1–65535, `LOG_LEVEL`/`DB_AUTO_MIGRATE` aren't one of their valid values, or a `DB_MAX_*_CONNS` isn't a positive integer. `DATABASE_URL` itself isn't format-checked — the PostgreSQL driver is the authority on what it accepts, so a bad value surfaces at connection time instead.

## Running Locally

```bash
git clone https://github.com/JonasBorgesLM/task-api.git
cd task-api
cp .env.example .env      # optional — sensible defaults apply without it
make run                   # or: go run ./cmd/api
```

```
{"time":"...","level":"INFO","msg":"server started","addr":":8080"}
```

With no `DATABASE_URL`, this uses the in-memory store — no external service needed, data doesn't survive a restart. For real PostgreSQL, see [Running with Docker](#running-with-docker) or point `DATABASE_URL` at any reachable instance.

```bash
HTTP_ADDR=:9090 go run ./cmd/api    # override a variable for a single run
make build && ./bin/task-api         # build a standalone binary
```

## Running with Docker

`Dockerfile` is a two-stage build: `golang:1.26.5-alpine` compiles a static (`CGO_ENABLED=0`) binary, copied into an empty `scratch` image — no shell, no libc, just the binary and a nonroot user.

```bash
make docker-build   # build the image

make docker-up       # full stack (API + PostgreSQL) via docker compose
curl http://localhost:8080/health
make docker-down     # stop and remove everything (data volume is kept)

make db-up            # PostgreSQL only — pair with `make run` on the host for faster edit/rebuild cycles
```

Run the built image standalone against any PostgreSQL instance:

```bash
docker run --rm -p 8080:8080 \
  -e DATABASE_URL="postgres://user:pass@host:5432/dbname?sslmode=disable" \
  task-api:latest
```

## Testing

| | Unit | Integration |
|---|---|---|
| Exercises | `Service`/`Handler` (fakes), `memoryRepository`, `config`, `middleware`, a full HTTP stack over the real `memoryRepository` | `postgresRepository` against **real PostgreSQL** |
| External dependency | None | PostgreSQL (`TEST_DATABASE_URL`) |
| Isolated by | Default build | `//go:build integration` on `task/postgres_repository_test.go` — not even compiled by a plain `go test ./...` |
| Speed | Milliseconds | Needs a live database |

```bash
make test               # unit tests
make test-race           # unit tests + race detector
make coverage             # unit tests + per-function coverage report

make db-up                # start PostgreSQL
make test-integration     # integration tests
make test-integration-race
```

`task/integration_test.go` (no build tag) is a full-stack HTTP test — real `Handler` → real `Service` → real `memoryRepository` — and despite the name has no external dependency; it's part of the unit suite because it validates that the layers are wired together correctly, not that PostgreSQL works. The dividing line for "integration" here is *needs a real external service*, not *spans more than one layer*.

Concurrency-sensitive paths (optimistic-concurrency conflicts) are exercised with real concurrent goroutines under `-race`, both against `memoryRepository` and against real PostgreSQL.

## Migrations

The `tasks` table schema lives in `task/migrations/0001_create_tasks_table.{up,down}.sql`, embedded into the binary (`embed.FS`) so it needs no files deployed alongside it. Applied migrations are tracked in `schema_migrations`; each migration runs inside its own transaction.

- **Automatic** (default, `DB_AUTO_MIGRATE=true`): applied on API startup whenever `DATABASE_URL` is set.
- **Manual**, via the standalone `cmd/migrate` CLI — for reverting (the server only ever migrates forward) or when `DB_AUTO_MIGRATE=false` (recommended with more than one replica, so migrations run once as an explicit deploy step instead of racing on every instance's boot):

```bash
make db-up            # if not already running
make migrate-up        # apply pending migrations
make migrate-down       # revert the last applied migration
```

Both are safe to re-run: `migrate-up` with nothing pending changes nothing; `migrate-down` with nothing to revert exits cleanly.

## API

All endpoints accept/return `application/json`; every response carries an `X-Request-Id` header for log correlation. **Full request/response schemas, validation rules, and examples: [docs/openapi.yaml](docs/openapi.yaml).**

| Method | Path | Description |
|---|---|---|
| `POST` | `/tasks` | Create a task |
| `GET` | `/tasks` | List tasks, oldest first (`?limit=`, `?offset=`) |
| `GET` | `/tasks/{id}` | Get a task by ID |
| `PUT` | `/tasks/{id}` | Update title/description |
| `PATCH` | `/tasks/{id}/done` | Mark as done (idempotent) |
| `DELETE` | `/tasks/{id}` | Delete a task |
| `GET` | `/health` | Liveness — always `200` while the process runs |
| `GET` | `/health/ready` | Readiness — `200` if the backing store is reachable, `503` if not |
| `GET` | `/debug/vars` | Runtime stats (`expvar`) — see [Observability](#observability) |

Errors always use the same envelope, `{"error": "description of the problem"}`. Common codes: `400` invalid input, `404` unknown task ID, `409` optimistic-concurrency conflict (re-fetch and retry), `500` unexpected failure (details logged server-side, never in the response).

**Quick walkthrough:**

```bash
ID=$(curl -s -X POST localhost:8080/tasks -H 'Content-Type: application/json' \
  -d '{"title":"Buy groceries","description":"Milk, eggs, bread"}' | jq -r .id)

curl -s localhost:8080/tasks/$ID                       # → 200, the task
curl -s "localhost:8080/tasks?limit=10&offset=0"        # → 200, [ ... ]
curl -s -X PATCH localhost:8080/tasks/$ID/done          # → 200, status: done
curl -s -o /dev/null -w '%{http_code}\n' \
  -X DELETE localhost:8080/tasks/$ID                    # → 204
```

## Graceful Shutdown

On `SIGINT`/`SIGTERM`: the server stops accepting new connections immediately, lets in-flight requests finish (bounded by `HTTP_SHUTDOWN_TIMEOUT`), then releases `Repository` resources (e.g. closes the PostgreSQL pool) — in that order, so a request in flight is never cut off from its database connection mid-shutdown. If requests don't finish before the timeout, shutdown returns an error and the process exits non-zero; a clean shutdown logs `shutdown completed` and exits `0`.

## Observability

- **Structured JSON logging** (`log/slog`), level controlled by `LOG_LEVEL`, one logger instance injected everywhere — no global mutable logger.
- **One access log line per request** (`middleware.Logging`): method, path, status, duration, request ID. This is the single source of truth for request outcomes — routine errors (`404`/`400`/`409`) aren't logged again elsewhere.
- **Request correlation** (`X-Request-Id`, `middleware.RequestID`): generated or propagated from the client, echoed in the response header, attached to every log line for that request.
- **Panic recovery** (`middleware.Recovery`): logs the panic and stack trace, returns a generic `500` — never leaks internals to the client.
- **Unexpected-error logging**: `Handler` logs, once, only errors it has no specific HTTP mapping for (genuine `500`s), with request ID/method/path attached.
- **`GET /debug/vars`**: stdlib `expvar` — command line, memory/GC stats, goroutine counts. Baseline visibility with no external dependency; see [Future Improvements](#future-improvements) for a richer metrics surface.

## Design Decisions

- **`Service` owns all business rules** (validation, ID generation, timestamps); `Handler` only translates HTTP; `Repository` only stores data. Title/description length is checked in Unicode characters (`utf8.RuneCountInString`), not bytes.
- **`Repository.FindAll` owns ordering and pagination**, not `Service` — so `postgresRepository` can push `ORDER BY … LIMIT … OFFSET …` into the query instead of fetching the whole table on every list request.
- **IDs and timestamps are assigned by `Service`**, never by storage or the client — `Repository` receives an already fully-formed `Task`; request bodies don't even expose these fields.
- **`PATCH /tasks/{id}/done` is idempotent** — calling it on an already-`done` task is a no-op read, never a write, so it can't conflict.
- **Optimistic concurrency via a `Version` field** (internal, never on the wire): `Update` rejects a write whose `Version` no longer matches the stored value with `ErrConflict` (`409`) instead of silently overwriting a concurrent change. `memoryRepository` does this with a compare-and-swap under its mutex; `postgresRepository` with `SELECT … FOR UPDATE` inside a transaction.
- **`context.Context` propagates end-to-end**, all the way into every `*sql.DB`/`*sql.Tx` call, so a canceled request aborts its in-flight query instead of running to completion for a client that's gone.
- **Request bodies are capped at 1 MiB** (`http.MaxBytesReader`) so an oversized payload is rejected instead of fully buffered.
- **PostgreSQL via `database/sql` + `pgx/v5`'s `stdlib` driver**, not `pgx`'s native pool: `*sql.DB`'s pooling/lifecycle primitives fully cover this project's needs, and `pgx` is pure Go (no cgo) — which is what lets `Dockerfile` build a static binary into `scratch`.
- **Migrations use a small embedded runner** (`task/postgres_migrate.go`), not a dedicated tool — a single-table schema didn't justify the dependency. `.up.sql`/`.down.sql` naming still follows the common convention.

Full rationale for each of these — including the PostgreSQL schema, indexing, and transaction design — lives in the code's own doc comments (start at `task/repository.go`, `task/postgres_repository.go`, and `task/migrations/0001_create_tasks_table.up.sql`).

## Future Improvements

- **Authentication and authorization** — JWT or API key middleware.
- **Application-level metrics** — `GET /debug/vars` gives baseline Go runtime stats; request count/latency/error-rate per route needs dedicated instrumentation (e.g. Prometheus).
- **Distributed tracing** — OpenTelemetry, for end-to-end request tracing.
- **Task filtering** — filter `GET /tasks` by status (the natural point to also add a `status` index).
- **`golangci-lint` in CI** — currently `staticcheck` only, because the `golangci-lint` build available at the time predates the Go version pinned in `go.mod`.
