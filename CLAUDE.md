# CLAUDE.md

Guidance for Claude Code (or any agent) working in this repository. Read this before making changes — it captures conventions and constraints that aren't obvious from the code alone.

## What this is

A small, production-shaped Go REST API for multi-user task management, with a swappable `Repository` (in-memory or PostgreSQL) behind one interface per domain. It's a reference project: the functional surface is intentionally minimal — the point is the layering, testing discipline, and operational details (config, migrations, graceful shutdown, health checks). Full narrative in [README.md](README.md); full rationale/architecture in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md); the decisions behind the non-obvious choices in [docs/DECISIONS.md](docs/DECISIONS.md); full API contract in [docs/openapi.yaml](docs/openapi.yaml).

## Decisões de arquitetura

Antes de implementar qualquer issue, leia [docs/DECISIONS.md](docs/DECISIONS.md) — ele registra
o raciocínio por trás de escolhas que não estão óbvias só lendo o código
(ordem de escrita no storage de anexos, por que CSRF não se aplica, etc.).
Se uma issue parecer contradizer uma decisão registrada lá, pare e pergunte
antes de prosseguir.

## Agent tooling in this repo

`.claude/` holds the shared agent foundation. It is tracked in git — it is
project documentation, not personal tooling. Only `.claude/settings.local.json`
(the per-developer permission allow-list) stays local.

**Rules** (`.claude/rules/*.md`) are path-scoped and load automatically when you
touch a matching file. They restate the invariants below, close to the code they
constrain: `go-layering`, `go-domain-errors`, `go-http-handlers`,
`go-repository-parity`, `go-tests`, `sql-migrations`, `config-env`,
`api-contract`, `attachment-storage`, `k8s-deploy`.

**Readers** (`.claude/agents/*.md`) are read-only subagents that distill the
large documents so the main context stays small — this file, `docs/DECISIONS.md`,
`docs/ARCHITECTURE.md` and the 1600-line `docs/openapi.yaml` are all bigger than
they are worth loading whole. Dispatch them in parallel:
`decisions-reader`, `architecture-reader`, `openapi-reader`, `invariants-reader`,
`test-surface-reader`.

**Skills** (`.claude/skills/*/SKILL.md`):

| Skill | Use it for |
|---|---|
| `/change-context` → `/change-validate` → `/change-plan` → `/implement-change` | the pipeline for a non-trivial change; artifacts land in `docs/changes/<slug>/` |
| `/decide "<need>"` | a question rather than a task — triages against `docs/DECISIONS.md` and is the only thing that writes to it |
| `/go-testing-guide` | what to test, at which layer, in which file, under which build tag |
| `/contract-sync` | drift between the code and the documents that describe it |
| `/pre-pr` | the gate before opening a PR |
| `/change-pipeline` | the shared conventions the stages reference |

Proportionality is built in: a typo needs no pipeline, a contained fix needs only
`/change-plan`, and the full sequence is for changes that touch the contract, the
schema, a dependency, or an invariant recorded here.

## Commands

Run `make help` for the full list. The ones you'll use most:

```
make check              # gofmt-check + vet + lint + race-tested unit tests — run before considering any change done
make test                # unit tests only, no external service
make test-integration    # PostgreSQL integration tests (needs `make db-up` first)
make run                 # start the API locally
```

Never propose `golangci-lint` for this repo unless you've confirmed its installed build supports the Go version pinned in `go.mod` — see `make lint`'s comment and `docs/ARCHITECTURE.md`'s Future Improvements. `staticcheck` is the linter actually wired into CI and `make lint`/`make check`.

## Architecture — do not violate this

Strict one-way dependency: `Handler → Service → Repository (interface)`. Each layer only knows the layer directly below it, and only through an interface. This applies **identically to both domain packages** — `internal/task` and `internal/user` — not just the original one.

- `Service` and `Handler` (in both `internal/task` and `internal/user`) must stay **completely unaware that PostgreSQL exists**. Only the standalone binaries in `cmd/` (`api`, `migrate`, `seed`) are allowed to import a PostgreSQL package (`pgx`, `database/sql` usage tied to Postgres specifics).
- **One-off operational CLIs live in their own `cmd/*` binary, never in `cmd/api`.** `cmd/migrate` and `cmd/seed` both follow this: a small `main.go` that loads `config.Config`, opens its own `*sql.DB`, and drives the domain packages' exported API — never wired into the HTTP server. `cmd/seed` in particular writes exclusively through `user.Service`/`task.Service` (`Register`, `CreateTask`/`TransitionStatus`), never straight to a `Repository` — that's what keeps seeded data subject to the exact same validation, ID generation, and timestamp rules as data created through the API. Follow this pattern for the next such tool rather than adding a flag/endpoint to `cmd/api`.
- Each domain package owns one `Repository` interface (`internal/task/repository.go`, `internal/user/repository.go`) — the only boundary its `Service` depends on. If you need new persistence behavior, add it to the relevant interface and implement it in **both** the memory and postgres implementations — never let one drift ahead of the other.
- `memoryRepository` (in both packages) is not legacy/placeholder code scheduled for deletion. It exists specifically so the unit suite has zero external dependencies. Keep it correct and in sync with `postgresRepository`'s contract.
- **Migrations are centralized in `internal/platform/migrate`**, not owned by `internal/task` — both domains share one `*sql.DB`, one `schema_migrations` bookkeeping table, and one embedded migration runner (`RunMigrations`/`RunMigrationsDown`). Add new migration files there, not inside a domain package. **A migration that adds a table also needs `internal/platform/migrate/migrate_test.go` updated**: `TestPostgres_RunMigrationsDown_RevertsMostRecentMigration` steps back from the newest migration *by name* and needs a new step for yours — `assertNewestMigrationIs` fails with that instruction rather than letting the mismatch surface as unrelated assertion noise — and the `tableExists` lists name the tables expected to exist. The teardown itself enumerates the schema, so it no longer has to be told about new tables.
- `task.Repository`'s methods that touch an existing task (`FindByID`, `FindAll`, `Delete`) take the owning `userID` and scope the operation to it; `Update` checks `task.UserID` against the stored row instead. **A task belonging to a different user is reported as `ErrNotFound`, never a distinct "forbidden" error** — this is deliberate (don't leak whether a given ID exists to a non-owner) and must be preserved in any new `Repository` method that touches a single task.
- `Repository.FindAll` owns ordering *and* pagination windowing (`limit`/`offset`), and — for `task.Repository` — the `userID` filter plus the status/priority ones — not `Service`. This is deliberate: it lets `postgresRepository` push `WHERE user_id = $1 AND status IN (…) ORDER BY … LIMIT … OFFSET …` into the SQL query instead of fetching the whole table (or another user's rows) on every `GET /tasks` call. If you touch listing/pagination/ownership filtering, keep it inside the query — don't reintroduce "fetch everything, filter/slice in Go" at the `Service` or `Handler` layer.
- **`FindAll`'s status/priority filters are sets, not single values** (`[]Status`/`[]Priority`): values within one field match with `OR`, the two fields combine with `AND`, and an empty slice means "no filter on this field". `Service.ListTasks` validates and de-duplicates before the repository ever sees them — that is what keeps `postgresRepository`'s `IN` list bounded by the enum's own size no matter how many times a caller repeats a query param. One unrecognized value rejects the whole request (`ErrInvalidInput` → `400`) rather than being dropped: filtering on just the values that happened to parse would return a plausible page answering a different question. Both `Repository` implementations must apply these rules identically — `memoryRepository` shares one `matchesAny` helper for exactly that reason, and the memory and PostgreSQL filter tests run the same OR-within/AND-across cases.
- **`internal/attachment` reaches ownership through the task, and never imports `internal/task`.** The `attachments` table has no `user_id`; every `Repository` method takes the acting `userID` and scopes through `tasks`. `postgresRepository` does it as a `JOIN` (and `Create` as an `INSERT ... SELECT`, so a write against somebody else's task inserts nothing); `memoryRepository` takes a `TaskOwnershipFunc` the composition root supplies, which is what keeps the import out. Both must answer identically: `ErrNotFound` for an attachment on another user's task, `ErrTaskNotFound` for a `Create` against one. Don't add a `user_id` column to `attachments` to "simplify" this — a second copy of the owner is a second thing that can disagree with the first.
- **Attachment bytes are written before the metadata row, and that order is deliberate.** The reverse leaves a row pointing at a missing file — a download that 500s forever. This order leaves at worst an unreferenced blob. `Service.Upload` cleans it up best-effort and returns the *original* error, not the cleanup one.
- **The content type is decided by `http.DetectContentType`, never by the client's declared header**, and the allow-list is applied to the detected value. Don't "fix" a rejected upload by trusting the declared type — that makes the allow-list decorative. `text/html` is excluded on purpose: served from this origin it would run as same-origin script.
- **Every download must keep `Content-Disposition: attachment`.** With `nosniff` (already global) it is what stops user-uploaded bytes rendering in this API's origin.
- **Never reach the filesystem except through the `pathguard.Guard`.** `Guard.Open`/`Guard.Create` are TOCTOU-safe handles; `Resolve` returns a string and explicitly is not one, so `Resolve` + `os.Open` reintroduces the symlink race. `FuzzFSBlobStore_OpenNeverEscapesRoot` guards this and runs in CI — if you change how storage keys are generated or how blobs are read, that target is the thing that will catch you.
- **`BlobStore` has two implementations and one set of assertions.** `runBlobStoreContract` (`internal/attachment/storage_contract_test.go`, deliberately untagged so both the unit and integration suites compile it) holds everything the interface promises; `fsBlobStore` and `s3BlobStore` each run all of it. Add a new promise there, not in one backend's test file — an fs-only assertion is one S3 can fail silently in production. Backend-specific properties (traversal containment, which belongs to paths and `pathguard`) stay in their own file.
- **`internal/config` imports nothing outside the standard library.** That is why `defaultHSTSMaxAge` is duplicated rather than imported from moat, and why the S3 settings are flat fields assembled into `attachment.S3Config` by `cmd/api` instead of being that type directly. Assembling domain types is the composition root's job.
- **A `storage_key` is an address, not a permission.** It is on the wire, it is a UUID (so nothing that parses as one can be a path), and every lookup that accepts one still re-checks task ownership. `original_filename` is metadata only and must never be used to build a filesystem path.
- **The contract is mounted under `/v1`; the operational routes are not.** `newServer` registers the domain handlers on a sub-mux with *unprefixed* patterns and mounts it with `http.StripPrefix("/v1", …)` — so handlers stay unaware of versioning and a v2 is a second mount, not an edit to every `RegisterRoutes`. `/health`, `/health/ready` and `/debug/vars` stay bare: probes and scrapers are operations, not clients, and the readiness path is named in deployment manifests. **Don't add an unversioned alias or a redirect** for the contract paths — that makes the prefix decorative and breaks the very callers versioning exists to protect. See `docs/DECISIONS.md`.
- Domain errors are sentinels per package (`task/errors.go`: `ErrNotFound`, `ErrInvalidInput`, `ErrAlreadyExists`, `ErrConflict`, `ErrInvalidTransition`; `user/errors.go`: `ErrNotFound`, `ErrInvalidInput`, `ErrAlreadyExists`, `ErrInvalidCredentials`), checked with `errors.Is`. Both `Repository` implementations within a package must return the *same* sentinel for the same condition — each package's `Handler.handleServiceError` is the only place that maps errors to HTTP status codes, and it must never grow a PostgreSQL-specific branch.

## Task status: transition rules live in Service, not the database

`task.Status` has four values (`pending`, `in_progress`, `done`, `cancelled`). The database `CHECK` constraint on the `status` column only fences the set of valid *values* — it encodes nothing about which *transitions* between them are legal. That's entirely `task.Service`'s job, via the `legalTransitions` table in `internal/task/service.go`:

```
from \ to     pending  in_progress  done  cancelled
pending          -          Y        Y        Y
in_progress      Y          -        Y        Y
done             Y          Y        -        N
cancelled        Y          N        N        -
```

Requesting a task's current status again is always a no-op success, independent of this table (mirrors `PATCH /tasks/{id}/done`'s pre-existing idempotency). An illegal transition returns `ErrInvalidTransition` (`409`), distinct from `ErrConflict` (also `409`, but for the unrelated optimistic-concurrency race). If you add a fifth status, update this table, the CHECK constraint (new migration, don't edit an applied one), `docs/openapi.yaml`'s enum, and `cmd/seed/data.go`'s `statusWeights` together — they must never drift apart.

## User accounts and authentication

- Authentication is **opaque bearer session tokens**, not JWT: `POST /auth/login` returns a random token (`crypto/rand`), of which only `sha256(token)` is ever persisted (`sessions.token_hash`) — a database leak alone can't yield a usable session. The raw token is never recoverable after the response that issued it.
- `user.RequireAuth` (`internal/user/middleware.go`) is the only auth-checking code in the codebase. It stores the authenticated user's ID and raw token in the request context via `middleware.ContextWithUserID`/`ContextWithSessionToken` (`internal/middleware/auth_context.go`) — generic context plumbing that itself knows nothing about users or tokens, preserving `middleware`'s "no domain knowledge" rule below.
- Authorization is **strict per-owner, no roles**: a user only ever sees/modifies their own tasks. Don't add an admin/shared-access path without being asked — see "Things not to do without being asked".
- `internal/task/handler.go`'s `RegisterRoutes` wraps every task route with `requireAuth`; `internal/user/handler.go`'s wraps `/auth/logout`/`/auth/me` with `requireAuth` and `/auth/register`/`/auth/login` with a shared `middleware.RateLimiter` instead (they're necessarily public, so rate limiting is what stands in for auth there).
- `user.Service.Authenticate` always calls `bcrypt.CompareHashAndPassword` once, even for an email that doesn't exist (against a fixed dummy hash — `dummyPasswordHash`) — **do not "optimize" this by short-circuiting before the bcrypt call**. That call is deliberately slow; skipping it for an unknown email reopens a timing side channel that lets a caller enumerate registered emails from response latency alone, despite the identical error message. See `docs/ARCHITECTURE.md`'s Authentication section.
- `user.Service.Register`/`Authenticate` normalize email (trim + lowercase, `normalizeEmail`) before it ever reaches `Repository`. The `users.email` `UNIQUE` constraint is case-sensitive at the schema level on purpose (normalization is `Service`'s job, not the database's) — don't "fix" this by making the constraint case-insensitive instead; that would just move the responsibility to the wrong layer.
- Expired sessions are cleaned up two ways, both intentional: lazily in `ValidateToken` (rejects and deletes on next use) and periodically via `user.Service.PruneExpiredSessions`, called by `cmd/api/main.go`'s `runPeriodicCleanup` goroutine every `sessionCleanupInterval`. Don't remove the periodic path thinking the lazy one covers it — it doesn't clean up sessions nobody ever tries to reuse.

## Conventions this codebase actually follows

- **User text is normalized in `Service` before it is validated**, via `moat/sanitize`: `PlainText` for a title (one line of display text), `StripControlChars` only for a description (its line breaks are content — `PlainText` would flatten paragraphs). Length is checked *after* normalization, so the limit describes what is stored. Don't extend this into stripping markup or Unicode category Cf: the first is not an XSS defense and corrupts legitimate values, and the second would remove the bidi isolates that Hebrew and Arabic text legitimately needs. See `validateTitleAndDescription`'s doc comment.
- **String length limits are measured in runes, not bytes** (`utf8.RuneCountInString`, see `task/service.go`). `len(string)` counts bytes and silently over-rejects non-ASCII input — this was a real, shipped bug here. Apply the same care anywhere else user text gets a length check. The one deliberate exception is `user.validatePassword`, which measures **bytes**: bcrypt itself operates on and rejects input over 72 bytes, so byte length is what actually matters there — see that function's doc comment.
- **Comments explain *why*, not *what*.** Every exported type/function has a doc comment, and it's usually there to record a non-obvious constraint or a rejected alternative (e.g. why `pgx`'s `stdlib` driver over its native pool, why migrations don't use a dedicated tool, why session tokens are hashed before storage). Match that style: don't add comments that just restate the code.
- **No speculative abstraction.** Don't add config options, interfaces, or layers for a need that doesn't exist yet. `docs/ARCHITECTURE.md`'s Future Improvements is the place for "we could add X later," not code.
- **Tests use fakes, not mocking frameworks** — see `fakeRepository`/`fakeService` in each package's `*_test.go`. Keep it that way; don't introduce a mocking library.
- **Unit vs. integration tests are strictly separated by build tag**, never by convention alone: each package's `postgres_repository_test.go` starts with `//go:build integration` and is excluded from a plain `go test ./...`. Any new PostgreSQL-dependent test goes in a file with that same tag. `internal/task/integration_test.go` (no tag) is a *different* thing — a full-stack HTTP test against the real `memoryRepository` (both task and user), with zero external dependencies; don't confuse the two or rename either in a way that blurs the distinction.
- **Concurrency-sensitive code gets a real concurrent test**, driven by actual goroutines racing each other and run under `-race` (see `TestConcurrentUpdate_LosersGetErrConflict` and its PostgreSQL counterpart) — not just sequential calls asserting the same thing.
- **`gofmt`, `go vet` (both default and `-tags=integration`), `staticcheck` (both) and `govulncheck` must be clean before you're done.** `govulncheck` runs once, default tags only — unlike vet/staticcheck, a second integration-tagged run would only surface vulnerabilities reachable from test helpers, which are not deployed. It fails the build only on a *reachable* vulnerability (its own default); an advisory on something in the dependency graph that nothing calls exits 0. See `docs/DECISIONS.md`. `make check` runs all of it except the PostgreSQL integration tests. CI enforces the same set — see `.github/workflows/ci.yml`.

## Kubernetes

- `k8s/` is a **disposable validation cluster**, not a production deployment: PostgreSQL and MinIO run in-cluster on `emptyDir`. Don't treat those two files as a template for anything real, and don't delete the warning comment on the Secret — it is base64, not encryption.
- **`HTTP_PRE_SHUTDOWN_DELAY` is load-bearing, and `terminationGracePeriodSeconds` must cover it plus `HTTP_SHUTDOWN_TIMEOUT`.** Raising either timeout without raising the grace period reintroduces dropped requests during a rollout — silently. See `docs/DECISIONS.md`.
- **`/health` is liveness and `/health/ready` is readiness; don't swap them.** Pointing liveness at the readiness route means a database blip restarts every pod, turning a recoverable outage into a crash loop that takes the API down with it.
- `k8s/rollout-test.sh` proves the rollout by measuring it, with load running inside the cluster. If you change the shutdown path, the probes, or the rollout strategy, run it — it caught a real bug that reading the manifests did not.

## Keep documentation in sync

- **README.md** is the human-facing quickstart/overview doc (what this is, why it's worth looking at, run it, configure it, test it). It intentionally does *not* duplicate the full API contract or the full design rationale.
- **docs/ARCHITECTURE.md** holds the full project structure tree, the detailed rationale behind each design decision, the status transition table's reasoning, and the Future Improvements/roadmap list.
- **docs/openapi.yaml** is the source of truth for the API contract — every endpoint, request/response schema, status code, and example. If you change a handler's behavior (new field, new status code, new query param), update this file in the same change.
- If you change a `Repository` interface, a config variable, or a `make` target, update the relevant README/docs/ARCHITECTURE.md section and this file in the same change — don't let them drift.

## Things not to do without being asked

- Don't add an admin role, shared/cross-user task access, a web framework, an ORM, or a router library — the project deliberately uses `net/http`'s `ServeMux` and the standard library, and authorization deliberately stays strict per-owner. See `docs/ARCHITECTURE.md`'s Future Improvements for what's intentionally deferred.
- Don't switch session auth to JWT, or add a second auth mechanism, without discussing it first — opaque server-side tokens were a deliberate choice (see "User accounts and authentication" above), not an oversight. **Exception, already discussed and recorded:** the cookie + Bearer dual mode in `docs/DECISIONS.md` § "Autenticação: modo duplo (cookie httpOnly + Bearer)" — both authenticate the same opaque token; it's a second transport, not a second mechanism.
- Rate limiting is `moat/ratelimit`, composed in three tiers in `cmd/api/main.go`'s `newServer` (global by address, tighter on `/auth/*`, per authenticated user) — see `docs/ARCHITECTURE.md`'s Rate limiting section. Two invariants there are load-bearing and easy to break: the per-user limiter must stay *inside* `requireAuth` (its key is the user ID that `RequireAuth` puts in the context, so moving it out leaves it with no key at all), and `GET /health`/`GET /health/ready` must stay on the outer mux, exempt from the global tier — a probe answered `429` gets read by an orchestrator as an unhealthy replica and kills a process that was serving fine. The store is in-process/single-instance; don't silently swap it for a distributed one (Redis, etc.), that's a real architectural change worth its own discussion.
- **Never key a rate limiter on `X-Forwarded-For` (or `X-Real-IP`) without checking the peer first.** The client writes those headers, so an unconditional read gives every request a fresh bucket — which does not look like a bug, it looks like a limiter that never fires. `TRUSTED_PROXIES` is the only thing that makes a forwarded header usable, and `moat/realip` then walks it right to left (the end your own proxy appended). The list names *your proxies*, never your clients, and the default route is rejected at startup.
- **`Strict-Transport-Security` is sent unconditionally, and must never carry `includeSubDomains` or `preload`.** The unconditional part reversed an earlier decision here, so don't "restore" the old opt-in shape: withholding the header, or deciding per request from `r.TLS`, disables HSTS behind the TLS-terminating proxy it exists for, because `r.TLS` is nil there even for requests the client made over HTTPS. `HSTS_MAX_AGE=0` is the documented opt-out and omits the header (never sends `max-age=0`, which tells browsers to forget an existing policy). `includeSubDomains`/`preload` stay off because both reach past this service to hosts it knows nothing about and `preload` is effectively irreversible. Full reasoning in `docs/ARCHITECTURE.md`'s Operational Behavior.
- **Don't loosen the Content-Security-Policy to `moat`'s default.** `secureheaders.DefaultCSP` is `default-src 'self'`; this API passes `WithCSP` with `default-src 'none'` plus explicit `base-uri`/`form-action`/`frame-ancestors`, which is strictly tighter and correct for a JSON-only API that serves no documents. Adopting the library default would be a silent regression.
- Don't add a dependency lightly. The direct runtime dependencies are `github.com/jackc/pgx/v5` (PostgreSQL), `golang.org/x/crypto` (bcrypt), `github.com/JonasBorgesLM/moat` (rate limiting, response security headers, sanitize/validate, pathguard, realip) and `github.com/minio/minio-go/v7` (S3-compatible attachment storage — see `docs/DECISIONS.md` § "Cliente de object storage: minio-go" for why this one over `aws-sdk-go-v2`, and what was accepted to bring it in). Together with their own transitive requirements they are 24 third-party modules total (`go list -deps -f '{{if not .Standard}}{{.Module}}{{end}}' ./cmd/api | sort -u` is the exact, current count — cross-check it before citing a number here, since it changes independently of this file). What actually has to stay true is narrower than a module count: **every one of them is pure Go, no cgo** — that's what lets `Dockerfile` build a static binary into `scratch`, and it's the one property a new dependency must preserve. Prefer `moat`'s zero-dependency packages over its separate store modules unless a shared backend is actually needed. `github.com/JonasBorgesLM/crier/{core,exporters/otlp}` (see `cmd/api/crier.go`) is opt-in the same way the S3 attachment backend is: compiled into every binary, but functionally inert — no exporter constructed, nothing imported at runtime beyond the package init — unless `CRIER_OTLP_ENDPOINT` is set. It adds exactly 4 modules to the graph (`crier/core`, `crier/exporters/otlp`, `go.opentelemetry.io/proto/slim/otlp`, `google.golang.org/protobuf`), all pure Go — the `slim` OTel proto variant exists specifically to avoid pulling in the full collector/gRPC dependency tree. See `docs/DECISIONS.md`'s Fase 11 section for the full accounting.
- Don't skip or weaken CI checks (`--no-verify`, disabling a lint rule, `-short` skips) to make something pass — fix the underlying issue.
- Don't commit `.env` (it's real local config, gitignored) — only `.env.example` with placeholder values is tracked.
