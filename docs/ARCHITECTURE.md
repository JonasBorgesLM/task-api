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
│   ├── attachment/
│   │   ├── attachment.go             # Domain model: Attachment (metadata only — the bytes live outside the database)
│   │   ├── errors.go                 # Domain error sentinels
│   │   ├── repository.go             # Repository interface + TaskOwnershipFunc — ownership reached through the task
│   │   ├── memory_repository.go      # In-memory implementation (ownership via the injected TaskOwnershipFunc)
│   │   ├── postgres_repository.go    # PostgreSQL implementation (ownership as a JOIN through tasks)
│   │   ├── postgres_repository_test.go   # Integration tests — build-tagged `integration`
│   │   ├── storage.go                # BlobStore: the bytes, behind a pathguard.Guard
│   │   ├── storage_test.go           # Traversal/symlink containment + FuzzFSBlobStore_OpenNeverEscapesRoot
│   │   ├── service.go                # Upload/Download/ListByTask: allow-list, key generation, size limit
│   │   └── handler.go                # POST/GET /tasks/{id}/attachments, GET /files/{key}
│   ├── config/                # Environment variable loading and validation (the only package that reads os.Getenv)
│   ├── middleware/             # RequestID, Logging, Recovery, CORS, generic context helpers — no domain knowledge
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
├── docker-compose.yml      # Local stack: PostgreSQL, the API, and a Swagger UI for manual testing
├── Dockerfile              # Multi-stage build → static binary on `scratch`
├── Makefile                # `make help` for the full command list
├── CLAUDE.md               # Guidance for AI agents / contributors working in this repo
└── .env.example
```

## Design Decisions

- **Input is normalized, and normalization is not validation.** `Service` runs user text through `moat/sanitize` before validating its length. A title gets `PlainText` (strip Unicode category Cc, collapse whitespace runs) because it is one line of display text; a description gets only `StripControlChars`, because its line breaks are content — flattening a multi-paragraph description would destroy data the user deliberately entered. Length is checked *after* normalization, so the limit applies to what is actually stored. What this does not do is strip markup: `Ben <the> Third` is stored verbatim, because encoding at the point of rendering is the only real answer to injection and mangling input here would corrupt legitimate values while protecting nothing. It also does not touch Unicode category Cf, which includes the bidirectional overrides behind Trojan Source-style spoofing — the same block contains the isolates that are the correct way to embed Hebrew or Arabic in Latin text, so a blanket strip would corrupt exactly the users who need them; a rule for that has not been made yet, and `TestCreateTask_LeavesBidirectionalFormatCharacters` records the gap so it cannot change unnoticed.
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

### API versioning: the contract is versioned, the operational surface is not

Every route a client codes against lives under `/v1`. The health probes (`/health`, `/health/ready`) and `/debug/vars` do not, and that split is the decision rather than an oversight.

An orchestrator probe does not negotiate an API version — it is configured once, in a deployment manifest, by whoever runs the service rather than by whoever consumes it. `docs/DECISIONS.md` already commits the readiness probe to `/health/ready`; putting it behind the version prefix would mean re-editing those manifests every time the API version moves, for no benefit to anyone. The same argument covers `/debug/vars`: a metrics scraper is operations, not a client.

**The unprefixed paths are not served at all.** There is no dual mount and no redirect. A compatibility alias would make the prefix decorative — clients would keep working against the unversioned paths, and the first genuine v2 would break exactly the callers the versioning was supposed to protect. `TestIntegration_Versioning_ContractIsUnderV1` asserts both halves: the `/v1` path answers, and the bare path 404s.

**Handlers do not know about the prefix.** `newServer` builds a sub-mux, registers the domain handlers on it with their unprefixed patterns (`POST /tasks`, not `POST /v1/tasks`), and mounts it with `http.StripPrefix("/v1", …)`. A v2 is therefore a second sub-mux and a second mount, not an edit to every `RegisterRoutes` in the codebase. `StripPrefix` is what keeps `r.PathValue` working: the sub-mux still matches `/tasks/{id}` and populates the path values as it would unmounted.

The mount pattern is `"/v1/"` with the trailing slash — a subtree pattern, so an unknown path beneath it 404s from inside the versioned mux instead of falling through.

### Attachments: ownership is a chain, not a copy

An attachment belongs to a task, and that task belongs to a user. `attachments` therefore carries **no `user_id` of its own** — duplicating the owner would create a second copy of a fact that can then disagree with the first, and the disagreement would be an authorization bug rather than a stale display.

Every `attachment.Repository` method takes the acting `userID` and reaches the owner through that chain. The two implementations express the same rule differently, and that asymmetry is deliberate:

- `postgresRepository` makes it a `JOIN tasks`, so the ownership rule and the lookup are one operation. `Create` is an `INSERT ... SELECT` whose `SELECT` produces a row only when the task exists *and* belongs to the caller, so hanging an attachment off somebody else's task inserts nothing — rather than being caught by a separate check that a later edit could reorder or skip.
- `memoryRepository` cannot join against a table it does not have, so it takes a `TaskOwnershipFunc` at construction. The consumer supplies the check, which is what keeps `internal/attachment` from importing `internal/task` and violating the one-way dependency rule.

What must **not** differ is the answer: an attachment on somebody else's task is `ErrNotFound`, and a `Create` against somebody else's task is `ErrTaskNotFound` — never a distinct "forbidden", for the same reason tasks work that way. `NewMemoryRepository(nil)` panics rather than defaulting to "allow", so an ownership check cannot go missing quietly and leave the unit suite passing.

**A storage key is not a capability.** The download route addresses files by `storage_key`, and that key is on the wire. Possessing one grants nothing on its own: the lookup resolves the key to its row and still re-checks that the caller owns the task behind it. `storage_key` is a UUID rather than free text specifically so the type itself rejects anything that could be a path — no separator, dot segment, or traversal sequence survives a UUID parse — and `original_filename` is retained as metadata that is never used to build one.

**Two backends, one interface, one set of assertions.** `fsBlobStore` writes files through a `pathguard.Guard`; `s3BlobStore` writes objects through `minio-go`. Neither `Service`, `Handler`, `Repository`, nor any test above the storage layer changed when the second one was added — which was the bet made when metadata and bytes were split into separate boundaries, and is the return on it.

They exist for genuinely different deployments rather than as a preference. The filesystem store needs no service at all, which is what keeps local development and the unit suite cheap; it cannot back a deployment that has to survive a rolling update, because a pod's local disk is not shared with the pod replacing it and is gone if the pod moves node. `cmd/api`'s `buildBlobStore` is the single place that picks between them, the same role `buildRepository` plays for persistence, and `config.Load` rejects having both configured — whichever lost would hold files the running process cannot see, and that surfaces as missing attachments rather than as a configuration error.

**The contract is tested once, against both.** `runBlobStoreContract` (in `storage_contract_test.go`, deliberately untagged so both suites compile it) holds every assertion the interface promises, and each implementation runs all of them. An fs-only assertion is one the S3 store can quietly fail in production, where nobody is looking. Backend-specific properties stay in their own files — traversal containment belongs to the filesystem store, because it is a property of paths and of `pathguard`.

**`minio-go` rather than the AWS SDK**, for reasons recorded in `docs/DECISIONS.md`. The short version: one direct require instead of several, and one client that addresses MinIO in development and S3 in production, so neither environment exercises a path the other never does. Dependency size was *not* the reason — measured, the two are within one module of each other.

**Blobs are reclaimed by a collector, not by the delete.** `ON DELETE CASCADE` removes attachment *rows* when their task is deleted, and nothing in that path reaches the filesystem. Rather than coupling the success of deleting a task to the success of deleting a file, a periodic sweep (`Service.CollectOrphans`, run from `cmd/api`'s `runPeriodicCleanup`) removes blobs that no row references. That is the same cost reasoning as writing bytes before metadata: an orphaned blob costs disk and nothing else, so prefer the failure mode that leaves garbage over the one that blocks an operation.

**The grace period is what makes that safe.** `ATTACHMENT_ORPHAN_MIN_AGE` (1h by default) is not a tuning knob. Upload writes the bytes before the metadata row, so in the window between those two steps a healthy upload is byte-for-byte indistinguishable from an orphan. A collector without a grace period would race every upload in flight and delete some of them — intermittently, under load, which is the worst way to find out. `CollectOrphans` rejects a non-positive age outright rather than letting a caller opt out of the margin, and `TestCollectOrphans_SparesBlobsInsideTheGracePeriod` pins it.

The sweep also refuses to delete anything it cannot positively identify as its own: `Repository.UnreferencedKeys` filters candidates to well-formed storage keys, so a stray file in the storage directory survives. Every ambiguity in code that deletes things resolves toward keeping them.

### Attachment storage: two boundaries, and the order between them

Metadata and bytes are separate boundaries — `Repository` and `BlobStore` — because they fail independently and are backed by entirely different things. Losing a row and losing a file are different incidents with different recoveries.

**Bytes are written first, metadata second**, and the order is a choice rather than an accident. The reverse would leave a row pointing at a file that does not exist: a download that 500s forever with nothing to indicate why. This order can leave an unreferenced blob instead, which costs disk and nothing else. `Service.Upload` deletes the blob when the metadata write fails, on a best-effort basis: if that cleanup also fails, the original error is what the caller sees, because losing it to report a cleanup problem would hide the actual cause.

**The content type comes from the bytes.** `http.DetectContentType` decides, and the declared `Content-Type` is ignored for the allow-list — a client writes that header, so trusting it would make the allow-list decorative. The detected type is what is stored, so a download describes what it is actually serving. The list is short and every entry is a format a browser will not execute in this origin; `text/html` is excluded deliberately, because attacker-authored HTML served from this API's origin would run as same-origin script.

**Downloads go out as `Content-Disposition: attachment`.** That, together with the `X-Content-Type-Options: nosniff` every response already carries, is what keeps user-supplied bytes from being rendered in this origin. The filename is encoded with `mime.FormatMediaType`, so a name containing a quote or a non-ASCII character cannot break out of the header value.

**Containment is the store's property, not the key format's.** Keys are server-generated UUIDs, so none of them *can* contain a separator or a dot segment today. The `pathguard.Guard` is not there because the keys are untrusted — it is there so that the safety of this code does not depend on that remaining true, and a future change to key generation cannot quietly turn this into a traversal bug. `Guard.Open`/`Guard.Create` enforce containment at the syscall layer, which is stronger than validating a string and then calling `os.Open`: the latter re-resolves every component and reopens exactly the symlink race the package exists to close. `FuzzFSBlobStore_OpenNeverEscapesRoot` asserts the invariant directly and runs in CI.

**The feature is opt-in** (`ATTACHMENT_STORAGE_DIR`), and with it unset the routes are not registered at all rather than existing and failing. There is no default path because the production image is a static binary on `scratch`, which has no writable filesystem — a default would produce a deployment that accepts uploads and then fails every one of them. The directory must already exist; the process does not create it, so a typo fails at startup instead of silently serving an empty tree.

### Authentication: opaque bearer session tokens, not JWT

`POST /auth/login` issues a random 256-bit token (`crypto/rand`). Only `sha256(token)` is ever persisted (`sessions.token_hash`); the raw token is returned to the client exactly once and is unrecoverable afterward. This was a deliberate choice over JWT:

- **Stdlib-only, no signing-key lifecycle to manage.** A JWT implementation needs either a new dependency or hand-rolled HMAC signing/verification — sessions need none of that.
- **Trivial to revoke.** `POST /auth/logout` is a single `DELETE … WHERE token_hash = …`. A stateless JWT can't be revoked before its expiry without reintroducing server-side state anyway (a blocklist), which erases most of the "stateless" advantage.
- **The cost is a DB read per authenticated request** (`FindSessionByTokenHash`) instead of pure in-process verification. For this project's scale, that's a fair trade for the simplicity and revocability above — see [Future Improvements](#future-improvements) for when this trade-off might flip.

Expiry is enforced by `user.Service.ValidateToken` comparing `ExpiresAt` to `time.Now()` on every use, not by a scheduled cleanup job on that path — an expired session is rejected (and lazily deleted) the next time it's used. This mirrors how `task`'s status `CHECK` constraint only fences the value set while `Service` owns the actual rules: the database doesn't know what "expired" means, only that a row exists or doesn't. A *separate*, genuinely periodic job (`user.Service.PruneExpiredSessions`, called every `sessionCleanupInterval` by `cmd/api/main.go`'s `runPeriodicCleanup`) exists purely to bound storage growth from sessions that are simply abandoned and never reused again — a closed browser tab, a token nobody ever sends back — which the lazy per-use check alone would never clean up.

**The email itself is validated with `moat/validate`, not by hand.** `validateEmail` runs `Required()`, `Email()` and `MaxLen(maxEmailLen)` against the *normalized* value, so what is checked is what will be stored and later looked up. It replaced a `strings.Contains(email, "@")` test that accepted `"@"`, `"a@"` and `"@b"` — enough to register an account at an address that can never receive anything. The rules also never echo the submitted value back in their message, which matters because a validation error reliably ends up in both a log line and a response body.

Note the deliberate asymmetry with passwords: `MaxLen` counts runes, which is correct for an email and wrong for a password, since bcrypt's 72-character ceiling is 72 *bytes*. `validatePassword` therefore keeps its own byte-based check.

**Email lookups are case-insensitive.** `user.Service.Register`/`Authenticate` both normalize (trim + lowercase) the email before it ever reaches `Repository` — `"User@Example.com"` and `"user@example.com"` are the same account. This has to happen in `Service`: the `users.email` `UNIQUE` constraint is case-sensitive at the schema level (see `TestPostgres_Schema_UniqueConstraintIsCaseSensitive`), so normalization only actually prevents duplicate accounts if it happens before the value is ever written.

**`Authenticate` burns the same CPU time whether the email exists or not.** An unknown email short-circuits before ever calling `bcrypt.CompareHashAndPassword` — but that call is deliberately slow, so skipping it would make "unknown email" measurably faster than "known email, wrong password," letting a caller enumerate registered emails purely from response timing despite the identical error message. The unknown-email branch instead runs the same comparison against a fixed dummy hash (`dummyPasswordHash`, computed once at package init) before returning, closing that gap. This is about function of time, not a promise of exact equality — don't be tempted to "verify" it with a timing assertion in a test; that's a well-known source of CI flakiness. The test suite only asserts the functional outcome (still `ErrInvalidCredentials` either way).

### Rate limiting

Three token-bucket tiers, all from [`moat/ratelimit`](https://github.com/JonasBorgesLM/moat) and all composed in `cmd/api/main.go`'s `newServer`:

| Tier | Key | Covers | Defaults |
|---|---|---|---|
| Global | client address | every route except the health probes | `RATE_LIMIT_BURST=60`, `RATE_LIMIT_PER_SEC=20` |
| Auth | client address | `POST /auth/register` and `POST /auth/login`, together | `AUTH_RATE_LIMIT_BURST=10`, `AUTH_RATE_LIMIT_PER_SEC=0.05` |
| User | authenticated user ID | every route requiring a token | `USER_RATE_LIMIT_BURST=120`, `USER_RATE_LIMIT_PER_SEC=40` |

**Token bucket, not a fixed window.** The previous in-house limiter was a fixed-window counter, which permits up to twice the intended rate across a boundary: a client spends its whole quota in the last instant of one window and its whole quota again in the first instant of the next. At the old 20-requests-per-5-minutes setting that is 40 login attempts in seconds. A bucket has no boundary to exploit while still allowing the short bursts a user-facing API wants.

**Why three tiers rather than one.** They are not redundant, and the ordering constraint is what forces the shape. The per-user tier can only run *after* `RequireAuth`, because the user ID it keys on does not exist before then — so it cannot bound the session lookup that `RequireAuth` itself performs. The global tier runs in front of everything and does bound it. The auth tier is separate because an unauthenticated caller at `/auth/login` can burn a bcrypt comparison per request, which deserves a much tighter budget than ordinary traffic.

**The health probes are exempt.** `GET /health` and `GET /health/ready` are registered on an outer mux whose catch-all routes everything else through the global limiter. A rate-limited probe is an availability bug waiting to happen: an orchestrator reads `429` as "this replica is unhealthy" and kills a process that was serving fine, turning a burst of client traffic into a restart loop. The probes are also the one caller that cannot choose to back off, they are cheap, and their volume is set by the orchestrator rather than by any client.

**The key is `RemoteAddr` unless a proxy is declared.** `X-Forwarded-For` and `X-Real-IP` are written by whoever sent the request, so keying on one unconditionally lets a client mint a fresh identity per request and bypass the limit entirely — the peer of the TCP connection is the only client identity the server actually knows.

That default is safe but blunt behind a reverse proxy, where the peer is always the proxy and every client therefore collapses into one bucket. `TRUSTED_PROXIES` (CIDRs or bare addresses) resolves it: when the peer is one of them, `moat/realip` reads the forwarding header and walks it **right to left**, because a proxy appends the address it received from — the rightmost entry was written by your own infrastructure, while the leftmost is whatever the original client chose to send. Reading left to right is the classic bug and hands the client the result.

Both address-keyed tiers share one key function, so trusting a proxy for one and not the other is not expressible. The list describes *your proxies*, never your clients: a client range in it hands those clients their own choice of bucket, which is why `0.0.0.0/0` and `::/0` are rejected at startup rather than accepted as configuration.

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
- **CORS (`middleware.CORS`)**: disabled by default (unset `CORS_ALLOWED_ORIGINS`) when running `go run ./cmd/api` directly — no `Access-Control-*` headers, no behavior change for the server-to-server/same-origin clients this API served exclusively before CORS support existed. `docker-compose.yml` sets it to `http://localhost:8082` by default instead, matching the Swagger UI it bundles at that port, so "Try it out" works with no extra setup — see README's "Swagger UI" section. Positioned inside `Logging` but outside `Recovery` in the middleware chain (`cmd/api/main.go`) so a preflight `OPTIONS` request it answers directly still gets an access log line, and so the `Access-Control-Allow-Origin` header it sets survives even a handler panic. It intercepts `OPTIONS` ahead of `mux` dispatch specifically because a route registered only for `GET`/`POST`/etc. has no `OPTIONS` handler of its own — an unhandled preflight would otherwise `404`.
- **Response security headers (`moat/secureheaders`)**: the library's defaults, with the Content-Security-Policy replaced by `default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`. That is tighter than the library's own default of `'self'`, and deliberately so: this API only ever returns JSON, so no document it serves legitimately loads a script, stylesheet, image or font, and `'none'` denies every fetch directive at once. `base-uri`, `form-action` and `frame-ancestors` are named explicitly because none of them inherits from `default-src`. It still matters despite the JSON content type, because a browser coaxed into rendering a response as a document — a MIME-sniffing bug, a `Content-Type` an intermediary rewrote — then meets a policy forbidding it to execute whatever it found. The middleware sits outside `CORS` in the chain, and therefore outside everything that can answer without reaching the mux: a preflight `CORS` answers itself, a `429` from the rate limiter, `Recovery`'s `500` after a panic, and the router's own `404`/`405` are exactly the responses that would otherwise go out bare.
- **HSTS is sent by default, and `HSTS_MAX_AGE=0` is the opt-out.** This reverses an earlier decision in this project, so the reasoning is worth stating in full. Per [RFC 6797 §7.2](https://www.rfc-editor.org/rfc/rfc6797#section-7.2) a user agent MUST ignore `Strict-Transport-Security` received over anything but a secure transport, so the header is inert — not harmful, inert — on the plaintext this binary serves. The earlier reading took that same fact as a reason to withhold it, on the grounds that only the operator knows whether TLS is really in front. The flaw in that position is the alternative it implies: deciding per request from `r.TLS` looks careful and is actively wrong here, because TLS terminates at a proxy and `r.TLS` is nil even for requests the client genuinely made over HTTPS — it disables HSTS in exactly the deployment it exists for. Sending it unconditionally is inert where it does not apply and correct where it does. What is *not* sent is `includeSubDomains` or `preload`: both reach past this service to every host under the domain, `preload` is in practice irreversible, and an operator who wants HSTS for this host alone must be able to say so. `HSTS_MAX_AGE=0` omits the header entirely rather than sending `max-age=0`, which would instruct browsers to forget a policy earlier responses established.

## Future Improvements

Deliberately deferred — either not yet needed at this project's scale, or a decision that deserves its own discussion before being made:

- **Malformed task IDs should be `400`, not `500`** — the API performs no UUID validation of its own, so a malformed `{id}` reaches PostgreSQL, whose `::uuid` cast rejects it as a query error and surfaces as `500` (see `TestPostgres_FindByID_MalformedID`, which currently pins that behavior). Two problems: client-supplied garbage is being reported as a server fault, which also pollutes `ERROR` logs and any 5xx alerting; and the two `Repository` implementations disagree — the in-memory store returns `404` for the same input. The fix is to validate the ID shape once, above the repositories (so both agree), and map it to the same `404` an unknown ID already produces — deliberately *not* a distinct "malformed" error, which would leak that a well-formed ID exists. Documented as-is in [docs/openapi.yaml](openapi.yaml) until then, since that document describes what the code does rather than what it should.
- **Application-level metrics** — `GET /debug/vars` gives baseline Go runtime stats; request count/latency/error-rate per route needs dedicated instrumentation (e.g. Prometheus).
- **Distributed tracing** — OpenTelemetry, for end-to-end request tracing.
- **Task filtering and search** — filter `GET /tasks` by `status`/`priority`, or search by title (the natural point to also add matching indexes).
- **Distributed rate limiting** — all three tiers use `moat/ratelimit`'s in-process store, so each replica enforces its own budget and the effective limit behind a load balancer is N times the configured one. `ratelimit.WithStore` takes a shared backend (the library ships a Redis one as a separate module) for a deployment where that matters.
- **API versioning** (`/v1/tasks`) — worth introducing before shipping a real external client (mobile app, third-party integration) that can't update in lockstep with the API.
- **BFF (Backend-for-Frontend) layer** — not justified yet with a single core resource, but the natural next architectural step once there's more than one downstream service to aggregate, or once a web client and a mobile client need meaningfully different payload shapes (dashboard aggregation vs. lean/offline-friendly responses). The groundwork is already compatible: stable core API, token-based auth, ownership already enforced server-side.
- **Reconsidering session tokens vs. JWT** — worth revisiting if/when multiple independently-deployed services need to verify a caller's identity without a shared database, which is exactly the case a stateless token is built for (see "Authentication" above for the trade-off as it stands today).
- **Admin/shared-access role** — see "Ownership model" above; deliberately not built until there's a concrete need for it, since it's a strictly larger authorization surface than "strict per-owner."
- **`golangci-lint` in CI** — currently `staticcheck` only, because the `golangci-lint` build available at the time predates the Go version pinned in `go.mod`.
