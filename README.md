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

The `main.go` acts as the **Composition Root**: it instantiates all concrete types and wires dependencies together. No layer instantiates its own dependencies.

## Requirements

- Go 1.22 or later (uses `http.ServeMux` path parameters and `log/slog`)
- No external dependencies — standard library only

## Getting Started

### Clone

```bash
git clone https://github.com/JonasBorgesLM/task-api.git
cd task-api
```

### Run

```bash
go run .
```

The server starts on port `8080` by default:

```
{"time":"...","level":"INFO","msg":"server started","addr":":8080"}
```

### Run with custom configuration

```bash
APP_PORT=9090 APP_SHUTDOWN_TIMEOUT=30s go run .
```

### Build

```bash
go build -o task-api .
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

| Variable | Description | Default | Example |
|---|---|---|---|
| `APP_PORT` | TCP port the HTTP server listens on | `8080` | `APP_PORT=9090` |
| `APP_SHUTDOWN_TIMEOUT` | Maximum time to wait for in-flight requests during shutdown | `10s` | `APP_SHUTDOWN_TIMEOUT=30s` |

**Validation rules:**

- `APP_PORT` must be an integer between 1 and 65535. Port 0 is rejected.
- `APP_SHUTDOWN_TIMEOUT` must be a valid Go duration string (e.g. `10s`, `1m`, `500ms`) and must be positive.

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
  "ID": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "Title": "Buy groceries",
  "Description": "Milk, eggs, bread",
  "Status": "pending",
  "CreatedAt": "2026-08-08T12:00:00Z",
  "UpdatedAt": "2026-08-08T12:00:00Z"
}
```

**HTTP status codes**

| Code | Reason |
|---|---|
| `201` | Task created successfully |
| `400` | Missing or empty title, or malformed JSON |
| `500` | Unexpected server error |

---

### `GET /tasks`

Returns all tasks. Returns an empty array when no tasks exist.

**Response** `200 OK`

```json
[
  {
    "ID": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
    "Title": "Buy groceries",
    "Description": "Milk, eggs, bread",
    "Status": "pending",
    "CreatedAt": "2026-08-08T12:00:00Z",
    "UpdatedAt": "2026-08-08T12:00:00Z"
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
  "ID": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "Title": "Buy groceries",
  "Description": "Milk, eggs, bread",
  "Status": "pending",
  "CreatedAt": "2026-08-08T12:00:00Z",
  "UpdatedAt": "2026-08-08T12:00:00Z"
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
  "ID": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "Title": "Buy groceries",
  "Description": "Milk, eggs, bread",
  "Status": "done",
  "CreatedAt": "2026-08-08T12:00:00Z",
  "UpdatedAt": "2026-08-08T12:05:00Z"
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
├── main.go               # Composition Root: wires dependencies and starts the server
├── health.go             # GET /health handler
├── main_test.go          # Tests for server lifecycle and health endpoint
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
`Repository` is defined as a Go interface. `main.go` injects the concrete `memoryRepository` at startup. Replacing it with a database implementation requires only a new file that satisfies the interface — no other code changes.

**UUID generated in the Service**
The `Service` generates a UUID v4 using `crypto/rand` from the standard library. The `Repository` receives a fully-formed `Task` and never assigns IDs. This keeps ID generation testable and independent of storage.

**Timestamps managed by the Service**
`CreatedAt` is set once at creation and never modified. `UpdatedAt` is refreshed on every mutation. The HTTP client cannot set or override either field — the request structs simply do not include them.

**`CompleteTask` is idempotent**
Calling `PATCH /tasks/{id}/done` on a task that is already `done` returns the current task without modifying `UpdatedAt` or calling `Repository.Update`. This follows REST conventions and makes the operation safe to retry.

**`MemoryRepository` is thread-safe**
The in-memory store uses `sync.RWMutex`. Multiple concurrent reads are allowed; writes are exclusive. All methods are safe to call from concurrent goroutines.

**Domain errors are translated at the Handler layer**
`ErrNotFound` becomes HTTP 404. `ErrInvalidInput` becomes HTTP 400 with the original message. All other errors become HTTP 500 with a generic message, and the original error is logged server-side.

**Structured logging with `log/slog`**
The application uses `log/slog` (Go standard library) with a JSON handler. The logger is created in `main.go` and injected into the `Handler`. No global mutable logger exists.

## Graceful Shutdown

The server handles `SIGINT` (Ctrl+C) and `SIGTERM` (sent by process managers and container runtimes):

1. The signal is received on a buffered channel via `signal.Notify`.
2. `http.Server.Shutdown(ctx)` is called with a context bounded by `APP_SHUTDOWN_TIMEOUT`.
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
