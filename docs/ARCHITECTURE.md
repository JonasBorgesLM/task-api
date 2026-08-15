# Architecture

Deep-dive companion to [README.md](../README.md): the full project structure, the reasoning behind every non-obvious design decision, and what's deliberately deferred. If you just want to run the thing, the README is enough — come here when you need to know *why* it's built this way before changing it.

## Table of Contents

1. [Layering](#layering)
2. [Project Structure](#project-structure)
3. [Design Decisions](#design-decisions)
4. [Operational Behavior](#operational-behavior)
5. [Future Improvements](#future-improvements)

## Layering

Three layers per domain, each depending only on the layer below it, and only through an interface. There are two domains — `task` and `user` — and they're siblings: neither imports the other. The only place that knows both exist is the composition root (`cmd/api/main.go`) and the generic HTTP-context plumbing in `middleware`.

```
HTTP Request
     │
     ▼
┌─────────────────────────────┐
│  RequestID → Logging →       │  Cross-cutting, domain-agnostic
│  Recovery → (requireAuth)    │  (requireAuth wraps auth/logout,
└──────────────┬───────────────┘   auth/me, and every /tasks/* route)
               │
     ┌─────────┴─────────┐
     ▼                   ▼
┌──────────┐       ┌──────────┐
│  user.   │       │  task.   │   Handler: decode/encode, route registration
│  Handler │       │  Handler │
└────┬─────┘       └────┬─────┘
     ▼                   ▼
┌──────────┐       ┌──────────┐
│  user.   │       │  task.   │   Service: validation, business rules,
│  Service │       │  Service │   ID/timestamp assignment, status transitions
└────┬─────┘       └────┬─────┘
     ▼                   ▼
┌──────────┐       ┌──────────┐
│  user.   │       │  task.   │   Repository: interface only
│Repository│       │Repository│
└────┬─────┘       └────┬─────┘
     │ implemented by     │ implemented by
     ├─────────┐          ├─────────┐
     ▼         ▼          ▼         ▼
 memory     postgres   memory     postgres
(no ext.   (shared *sql.DB — one pool, one
 dep.)      schema_migrations table, both domains)
```

`cmd/api/main.go` is the **Composition Root** — the only place that instantiates concrete types and wires them together. `Service` and `Handler` in both domains depend only on their own package's `Repository` interface and are completely unaware PostgreSQL exists; only the standalone binaries in `cmd/` (`api`, `migrate`, `seed`) import a PostgreSQL package. Swapping `memoryRepository` for `postgresRepository` — or adding a third implementation, or a third domain — requires zero changes to business logic or HTTP handling.

`user.RequireAuth` is the one piece of domain-aware HTTP middleware in the codebase. It lives in `internal/user`, not in the generic `internal/middleware` package, specifically so `middleware` never has to import a domain package — it only exposes generic context plumbing (`ContextWithUserID`/`UserIDFromContext`, `ContextWithSessionToken`/`SessionTokenFromContext`) that `user.RequireAuth` writes to and `task.Handler` reads from.

## Project Structure

```
task-api/
├── cmd/
│   ├── api/
│   │   ├── main.go                   # Composition Root: wires both domains' Repository→Service→Handler, starts the HTTP server
│   │   ├── health.go                 # GET /health (liveness) and GET /health/ready (readiness — pings the shared *sql.DB)
│   │   └── *_test.go                 # Server lifecycle + full-stack HTTP tests
│   ├── migrate/
│   │   └── main.go                   # Standalone CLI: applies/reverts PostgreSQL migrations
│   └── seed/
│       ├── main.go                   # Standalone CLI: populates demo users + tasks via the real Services
│       └── data.go                   # Word lists + randomTask()/randomStatus()/randomPriority() generators
├── internal/
│   ├── config/                # Environment variable loading and validation (the only package that reads os.Getenv)
│   ├── middleware/             # RequestID, Logging, Recovery, RateLimiter, generic context helpers — no domain knowledge
│   ├── platform/
│   │   └── migrate/
│   │       ├── migrate.go            # Embedded migration runner (RunMigrations / RunMigrationsDown), shared by both domains
│   │       └── migrations/           # *.up.sql / *.down.sql, embedded into the binary
│   ├── task/
│   │   ├── task.go                   # Domain model: Task struct, Status/Priority types
│   │   ├── errors.go                 # Domain error sentinels
│   │   ├── repository.go             # Repository interface — ownership-scoped, the boundary Service depends on
│   │   ├── memory_repository.go      # In-memory implementation (sync.RWMutex, CAS-based optimistic concurrency)
│   │   ├── postgres_repository.go    # PostgreSQL implementation (*sql.DB, transactions, parameterized queries)
│   │   ├── postgres_repository_test.go   # Integration tests — build-tagged `integration`, need real PostgreSQL
│   │   ├── service.go                # Business logic: validation, ID/timestamp assignment, legalTransitions table
│   │   ├── handler.go                # HTTP handlers, route registration (wrapped in requireAuth), pagination parsing
│   │   └── integration_test.go       # Full-stack HTTP tests, both domains' real in-memory repositories, real auth flow
│   └── user/
│       ├── user.go                   # Domain model: User, Session
│       ├── errors.go                 # Domain error sentinels
│       ├── repository.go             # Repository interface (users + sessions — one store backs both)
│       ├── memory_repository.go      # In-memory implementation
│       ├── postgres_repository.go    # PostgreSQL implementation
│       ├── postgres_repository_test.go   # Integration tests — build-tagged `integration`
│       ├── service.go                # Register/Authenticate (bcrypt) + session issuance/validation (sha256(token))
│       ├── handler.go                # /auth/register, /auth/login, /auth/logout, /auth/me
│       └── middleware.go             # RequireAuth — the one domain-aware middleware in the codebase
├── docs/
│   ├── ARCHITECTURE.md     # this file
│   └── openapi.yaml        # Full API contract — every endpoint, schema, status code, example
├── docker-compose.yml      # Local stack: PostgreSQL, and optionally the API itself
├── Dockerfile              # Multi-stage build → static binary on `scratch`
├── Makefile                # `make help` for the full command list
├── CLAUDE.md               # Guidance for AI agents / contributors working in this repo
└── .env.example
```

## Design Decisions

- **`Service` owns all business rules** (validation, ID generation, timestamps, status transitions); `Handler` only translates HTTP; `Repository` only stores data. Title/description length is checked in Unicode characters (`utf8.RuneCountInString`), not bytes — the one deliberate exception is `user.validatePassword`, measured in bytes, because bcrypt itself operates on and rejects input over 72 bytes.
- **`Repository.FindAll` owns ordering, pagination, and (for `task`) the ownership filter** — not `Service` — so `postgresRepository` can push `WHERE user_id = … ORDER BY … LIMIT … OFFSET …` into the query instead of fetching the whole table (or another user's rows) on every list request.
- **IDs and timestamps are assigned by `Service`**, never by storage or the client — `Repository` receives an already fully-formed entity; request bodies don't even expose these fields.
- **`PATCH /tasks/{id}/done` is idempotent** — calling it on an already-`done` task is a no-op read, never a write, so it can't conflict. `PATCH /tasks/{id}/status` generalizes this: requesting the task's *current* status, whatever it is, is always a no-op success.
- **Optimistic concurrency via a `Version` field** (internal, never on the wire): `Update` rejects a write whose `Version` no longer matches the stored value with `ErrConflict` (`409`) instead of silently overwriting a concurrent change. `memoryRepository` does this with a compare-and-swap under its mutex; `postgresRepository` with `SELECT … FOR UPDATE` inside a transaction. That locking `SELECT`'s `WHERE` clause also filters by `user_id` (same as `FindByID`/`Delete`), so a caller updating a task it doesn't own never acquires the row lock at all — it falls straight into `ErrNotFound` instead of briefly contending with the real owner's own concurrent `Update` only to be rejected a moment later anyway.
- **`context.Context` propagates end-to-end**, all the way into every `*sql.DB`/`*sql.Tx` call, so a canceled request aborts its in-flight query instead of running to completion for a client that's gone.
- **Request bodies are capped at 1 MiB** (`http.MaxBytesReader`) so an oversized payload is rejected instead of fully buffered.
- **PostgreSQL via `database/sql` + `pgx/v5`'s `stdlib` driver**, not `pgx`'s native pool: `*sql.DB`'s pooling/lifecycle primitives fully cover this project's needs, and `pgx` is pure Go (no cgo) — which is what lets `Dockerfile` build a static binary into `scratch`.
- **Migrations use a small embedded runner** (`internal/platform/migrate`), not a dedicated tool — this project's schema didn't justify the dependency. `.up.sql`/`.down.sql` naming still follows the common convention. Migrations are centralized here, not owned by `task`, because `user` needs the same runner and the same `schema_migrations` bookkeeping table — two independent runners racing against the same table would be a correctness bug waiting to happen.

### Ownership model: strict per-user, no roles

Every task carries a `UserID`. Every `Repository` method that touches an existing task takes (or, for `Update`, checks against) the owning user, and filters at the query level — not "fetch then check in Go." A task that exists but belongs to someone else is reported identically to a task that doesn't exist at all: `ErrNotFound` → `404`. This is deliberate, not an oversight — returning a distinct "forbidden" response would let a caller learn that a given task ID exists even without access to it. There is no admin role or shared-access path; if that's ever needed, it's a new, explicit capability, not a relaxation of this rule (see [Future Improvements](#future-improvements)).

### Authentication: opaque bearer session tokens, not JWT

`POST /auth/login` issues a random 256-bit token (`crypto/rand`). Only `sha256(token)` is ever persisted (`sessions.token_hash`); the raw token is returned to the client exactly once and is unrecoverable afterward. This was a deliberate choice over JWT:

- **Stdlib-only, no signing-key lifecycle to manage.** A JWT implementation needs either a new dependency or hand-rolled HMAC signing/verification — sessions need none of that.
- **Trivial to revoke.** `POST /auth/logout` is a single `DELETE … WHERE token_hash = …`. A stateless JWT can't be revoked before its expiry without reintroducing server-side state anyway (a blocklist), which erases most of the "stateless" advantage.
- **The cost is a DB read per authenticated request** (`FindSessionByTokenHash`) instead of pure in-process verification. For this project's scale, that's a fair trade for the simplicity and revocability above — see [Future Improvements](#future-improvements) for when this trade-off might flip.

Expiry is enforced by `user.Service.ValidateToken` comparing `ExpiresAt` to `time.Now()` on every use, not by a scheduled cleanup job on that path — an expired session is rejected (and lazily deleted) the next time it's used. This mirrors how `task`'s status `CHECK` constraint only fences the value set while `Service` owns the actual rules: the database doesn't know what "expired" means, only that a row exists or doesn't. A *separate*, genuinely periodic job (`user.Service.PruneExpiredSessions`, called every `sessionCleanupInterval` by `cmd/api/main.go`'s `runPeriodicCleanup`) exists purely to bound storage growth from sessions that are simply abandoned and never reused again — a closed browser tab, a token nobody ever sends back — which the lazy per-use check alone would never clean up.

**Email lookups are case-insensitive.** `user.Service.Register`/`Authenticate` both normalize (trim + lowercase) the email before it ever reaches `Repository` — `"User@Example.com"` and `"user@example.com"` are the same account. This has to happen in `Service`: the `users.email` `UNIQUE` constraint is case-sensitive at the schema level (see `TestPostgres_Schema_UniqueConstraintIsCaseSensitive`), so normalization only actually prevents duplicate accounts if it happens before the value is ever written.

**`Authenticate` burns the same CPU time whether the email exists or not.** An unknown email short-circuits before ever calling `bcrypt.CompareHashAndPassword` — but that call is deliberately slow, so skipping it would make "unknown email" measurably faster than "known email, wrong password," letting a caller enumerate registered emails purely from response timing despite the identical error message. The unknown-email branch instead runs the same comparison against a fixed dummy hash (`dummyPasswordHash`, computed once at package init) before returning, closing that gap. This is about function of time, not a promise of exact equality — don't be tempted to "verify" it with a timing assertion in a test; that's a well-known source of CI flakiness. The test suite only asserts the functional outcome (still `ErrInvalidCredentials` either way).

### Rate limiting on authentication endpoints

`POST /auth/register` and `POST /auth/login` share one `middleware.RateLimiter` (fixed window, keyed by client IP — see `internal/middleware/rate_limit.go`), wired in only for those two routes in `cmd/api/main.go`. It exists to raise the cost of credential-stuffing and registration-spam against a single instance, not as a general-purpose API rate limit — every other route is unlimited.

It is deliberately an **in-process, single-instance** limiter: state lives in a Go map, not a shared store, so each replica in a multi-instance deployment enforces its own independent budget rather than one budget for the whole fleet. That's a real gap for a horizontally-scaled deployment (a distributed version would need something like Redis) — see [Future Improvements](#future-improvements) — but it's still a meaningful improvement over no limiting at all for this project's current single-instance shape, and the limiter's own stale entries are swept by the same `runPeriodicCleanup` goroutine that prunes expired sessions.

### Task status: four states, transitions owned by Service

Status is `pending` / `in_progress` / `done` / `cancelled`. The database `CHECK` constraint only fences the set of valid *values*; which *transitions* between them are legal is entirely `task.Service`'s responsibility, via the `legalTransitions` table in `internal/task/service.go`:

```
from \ to     pending  in_progress  done  cancelled
pending          -          Y        Y        Y
in_progress      Y          -        Y        Y
done             Y          Y        -        N
cancelled        Y          N        N        -
```

`done` and `cancelled` are both reopenable back to `pending` (and `done` can resume directly to `in_progress`), but a `cancelled` task must pass back through `pending` before becoming active again — keeping the graph small without blocking any realistic workflow. `pending → done` stays legal directly, preserving `PATCH /tasks/{id}/done`'s original one-hop behavior with no regression. Requesting the current status again is always a no-op success, independent of this table — the same idempotency `.../done` has always had, generalized. An illegal transition returns `ErrInvalidTransition`, mapped to `409` (distinct message from the concurrency-conflict `ErrConflict`, which also maps to `409`) — both mean "this request conflicts with the resource's current state," just for different reasons.

## Operational Behavior

**Graceful shutdown.** On `SIGINT`/`SIGTERM`: the server stops accepting new connections immediately, lets in-flight requests finish (bounded by `HTTP_SHUTDOWN_TIMEOUT`), then closes the shared `*sql.DB` — in that order, so a request in flight is never cut off from its database connection mid-shutdown. If requests don't finish before the timeout, shutdown returns an error and the process exits non-zero; a clean shutdown logs `shutdown completed` and exits `0`.

**Observability.**

- Structured JSON logging (`log/slog`), level controlled by `LOG_LEVEL`, one logger instance injected everywhere — no global mutable logger.
- One access log line per request (`middleware.Logging`): method, path, status, duration, request ID. This is the single source of truth for request outcomes — routine errors (`404`/`400`/`409`) aren't logged again elsewhere.
- Request correlation (`X-Request-Id`, `middleware.RequestID`): generated or propagated from the client, echoed in the response header, attached to every log line for that request.
- Panic recovery (`middleware.Recovery`): logs the panic and stack trace, returns a generic `500` — never leaks internals to the client.
- Unexpected-error logging: each domain's `Handler` logs, once, only errors it has no specific HTTP mapping for (genuine `500`s), with request ID/method/path attached.
- `GET /debug/vars`: stdlib `expvar` — command line, memory/GC stats, goroutine counts. Baseline visibility with no external dependency; see [Future Improvements](#future-improvements) for a richer metrics surface. Unlike the health routes it requires authentication: those must stay open because an orchestrator's probe carries no credentials, whereas whoever reads runtime internals can present a token.
- **Dependency failures are not reported as auth failures.** `user.RequireAuth` answers `401` only when the session lookup says the token is unknown or expired; if the lookup itself fails (database unreachable, timeout), it answers `503` and logs the cause at `ERROR`. Collapsing both into `401` would tell clients holding valid tokens to discard them — stampeding the login endpoint, which needs the same unavailable database — and would hide a total outage behind `WARN`-level "client auth errors" that no `5xx` alert would ever catch.

## Future Improvements

Deliberately deferred — either not yet needed at this project's scale, or a decision that deserves its own discussion before being made:

- **Malformed task IDs should be `400`, not `500`** — the API performs no UUID validation of its own, so a malformed `{id}` reaches PostgreSQL, whose `::uuid` cast rejects it as a query error and surfaces as `500` (see `TestPostgres_FindByID_MalformedID`, which currently pins that behavior). Two problems: client-supplied garbage is being reported as a server fault, which also pollutes `ERROR` logs and any 5xx alerting; and the two `Repository` implementations disagree — the in-memory store returns `404` for the same input. The fix is to validate the ID shape once, above the repositories (so both agree), and map it to the same `404` an unknown ID already produces — deliberately *not* a distinct "malformed" error, which would leak that a well-formed ID exists. Documented as-is in [docs/openapi.yaml](openapi.yaml) until then, since that document describes what the code does rather than what it should.
- **Application-level metrics** — `GET /debug/vars` gives baseline Go runtime stats; request count/latency/error-rate per route needs dedicated instrumentation (e.g. Prometheus).
- **Distributed tracing** — OpenTelemetry, for end-to-end request tracing.
- **Task filtering and search** — filter `GET /tasks` by `status`/`priority`, or search by title (the natural point to also add matching indexes).
- **Distributed rate limiting** — `middleware.RateLimiter` (see "Rate limiting on authentication endpoints" above) is in-process and single-instance; a horizontally-scaled deployment needs a shared store (e.g. Redis) instead, so every replica enforces the same budget rather than each having its own.
- **CORS** — needed the moment a browser-based frontend calls this API from a different origin; not needed for a server-to-server or same-origin client.
- **API versioning** (`/v1/tasks`) — worth introducing before shipping a real external client (mobile app, third-party integration) that can't update in lockstep with the API.
- **BFF (Backend-for-Frontend) layer** — not justified yet with a single core resource, but the natural next architectural step once there's more than one downstream service to aggregate, or once a web client and a mobile client need meaningfully different payload shapes (dashboard aggregation vs. lean/offline-friendly responses). The groundwork is already compatible: stable core API, token-based auth, ownership already enforced server-side.
- **Reconsidering session tokens vs. JWT** — worth revisiting if/when multiple independently-deployed services need to verify a caller's identity without a shared database, which is exactly the case a stateless token is built for (see "Authentication" above for the trade-off as it stands today).
- **Admin/shared-access role** — see "Ownership model" above; deliberately not built until there's a concrete need for it, since it's a strictly larger authorization surface than "strict per-owner."
- **`golangci-lint` in CI** — currently `staticcheck` only, because the `golangci-lint` build available at the time predates the Go version pinned in `go.mod`.
