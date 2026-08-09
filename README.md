# Task API

A small HTTP REST API for task management, built with Go using only the standard library.

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
│ Repository  │  Persists and retrieves tasks (in-memory implementation)
└─────────────┘
```

`cmd/api/main.go` acts as the **Composition Root**: it instantiates all concrete types and wires dependencies together. No layer instantiates its own dependencies.

## Requirements

- Go 1.22 or later (uses `http.ServeMux` path parameters and `log/slog`)
- No external dependencies — standard library only

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

If you don't create a `.env` file, the application falls back to its built-in defaults (`HTTP_ADDR=:8080`, 5s/10s/60s read/write/idle timeouts, `HTTP_SHUTDOWN_TIMEOUT=10s`).

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

**Validation rules:**

- `HTTP_ADDR` must be a syntactically valid `host:port` address (per `net.SplitHostPort`) with a numeric port between 1 and 65535. Port 0 is rejected.
- All `HTTP_*_TIMEOUT` variables must be a valid Go duration string (e.g. `10s`, `1m`, `500ms`) and must be strictly positive.

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
| `title` | string | yes | Task title. Cannot be empty or whitespace-only. |
| `description` | string | no | Optional task description. |

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

Returns all tasks. Returns an empty array when no tasks exist.

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
| `200` | Success (empty array `[]` if no tasks exist) |
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

**Response** `200 OK` — the updated task.

**HTTP status codes**

| Code | Reason |
|---|---|
| `200` | Task updated |
| `400` | Missing or empty title, or malformed JSON |
| `404` | No task with the given ID |
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

## Error Handling

All error responses use the same JSON envelope:

```json
{
  "error": "description of the problem"
}
```

| HTTP Code | Meaning |
|---|---|
| `400 Bad Request` | Invalid input — malformed JSON or failed business rule validation (e.g. empty title). The error message describes the specific problem. |
| `404 Not Found` | The requested task ID does not exist. |
| `409 Conflict` | A task with the generated ID already exists. Practically unreachable in normal operation, since IDs are server-generated UUIDv4. |
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
├── go.mod
├── config/
│   ├── config.go         # Environment variable loading and validation
│   └── config_test.go
└── task/
    ├── task.go           # Domain model: Task struct and Status type
    ├── repository.go     # Repository interface and ErrNotFound
    ├── memory_repository.go      # In-memory implementation of Repository
    ├── memory_repository_test.go
    ├── service.go        # Business logic: validation, ID generation, timestamps
    ├── service_test.go
    ├── handler.go        # HTTP handlers and route registration
    └── handler_test.go
```

## Design Decisions

**Service contains all business rules**
Validation (non-empty title), ID generation, and timestamp management live exclusively in the `Service`. The `Handler` translates HTTP; the `Repository` stores data. Neither layer contains domain logic.

**Repository is interface-based**
`Repository` is defined as a Go interface. `cmd/api/main.go` injects the concrete `memoryRepository` at startup. Replacing it with a database implementation requires only a new file that satisfies the interface — no other code changes.

**UUID generated in the Service**
The `Service` generates a UUID v4 using `crypto/rand` from the standard library. The `Repository` receives a fully-formed `Task` and never assigns IDs. This keeps ID generation testable and independent of storage.

**Timestamps managed by the Service**
`CreatedAt` is set once at creation and never modified. `UpdatedAt` is refreshed on every mutation. The HTTP client cannot set or override either field — the request structs simply do not include them.

**`CompleteTask` is idempotent**
Calling `PATCH /tasks/{id}/done` on a task that is already `done` returns the current task without modifying `UpdatedAt` or calling `Repository.Update`. This follows REST conventions and makes the operation safe to retry.

**`MemoryRepository` is thread-safe**
The in-memory store uses `sync.RWMutex`. Multiple concurrent reads are allowed; writes are exclusive. All methods are safe to call from concurrent goroutines.

**Domain errors are translated at the Handler layer**
`ErrNotFound` becomes HTTP 404. `ErrInvalidInput` becomes HTTP 400 with the original message. `ErrAlreadyExists` becomes HTTP 409 (practically unreachable, since IDs are server-generated UUIDv4). All other errors become HTTP 500 with a generic message, and the original error is logged server-side.

**Structured logging with `log/slog`**
The application uses `log/slog` (Go standard library) with a JSON handler. The logger is created in `cmd/api/main.go` and injected into the `Handler`. No global mutable logger exists.

**`context.Context` propagates from the HTTP request through Service and Repository**
Every `Repository` and `Service` method takes `ctx context.Context` as its first parameter, sourced from `r.Context()` in the `Handler`. The in-memory `Repository` checks `ctx.Err()` before each operation; this has no practical effect today, but it means a future database-backed `Repository` can honor request cancellation and timeouts without changing any method signature.

**Request bodies are capped at 1 MiB**
`POST /tasks` and `PUT /tasks/{id}` wrap the request body in `http.MaxBytesReader` before decoding JSON, so an oversized payload is rejected instead of being fully buffered into memory.

**No optimistic concurrency control on updates**
`UpdateTask` and `CompleteTask` perform a read-modify-write against the `Repository` (`FindByID` then `Update`) with no version check. Two concurrent requests updating the same task ID can race: both read the same starting state, and whichever `Update` call completes last silently overwrites the other's changes (a "lost update"). This is a known, accepted limitation for the current single-writer-per-task usage pattern — closing it would require a version field on `Task` and a conditional (compare-and-swap) `Update` in `Repository`.

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

- **Persistent storage** — replace `MemoryRepository` with a SQL or NoSQL database implementation
- **Authentication and authorization** — protect endpoints with JWT or API key middleware
- **Request ID middleware** — attach a unique ID to each request for log correlation
- **Metrics** — expose Prometheus metrics (request count, latency, error rate)
- **Distributed tracing** — integrate OpenTelemetry for end-to-end request tracing
- **Pagination** — add `limit` and `offset` parameters to `GET /tasks`
- **Task filtering** — filter tasks by status in `GET /tasks`
- **Docker** — add `Dockerfile` and `docker-compose.yml` for containerized development
- **CI/CD** — add GitHub Actions workflow for automated testing and building
