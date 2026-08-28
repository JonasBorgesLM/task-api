# Task API

A small, production-shaped multi-user task manager, written in Go — a compact reference for how a real Go service is put together: layered architecture, session-based authentication, a swappable persistence layer (in-memory or PostgreSQL, behind one interface per domain), structured logging, graceful shutdown, health/readiness checks, and a test suite that keeps unit and integration tests genuinely separate.

## Table of Contents

1. [Why this project](#why-this-project)
2. [Requirements](#requirements)
3. [Configuration](#configuration)
4. [Running Locally](#running-locally)
5. [Running with Docker](#running-with-docker)
6. [Testing](#testing)
7. [Kubernetes](#kubernetes)
7. [Migrations](#migrations)
8. [Seeding](#seeding)
9. [API](#api)
10. [Learn more](#learn-more)

## Why this project

Every task belongs to exactly one user, authenticated with a bearer session token (`POST /v1/auth/login`); tasks move through four states (`pending` → `in_progress`/`done`/`cancelled`, with real transition rules — not just a status field) and carry a priority. The functional surface stays intentionally small. What's worth looking at is what's *underneath* it:

- **Swappable persistence with zero business-logic coupling.** `Service` and `Handler` (in both the `task` and `user` domains) never import PostgreSQL — the in-memory store and the PostgreSQL store implement the exact same `Repository` interface, and swapping one for the other requires no change above the repository layer.
- **Real production concerns, already handled.** Graceful shutdown, liveness/readiness checks, structured JSON logs correlated by request ID, panic recovery, optimistic concurrency control on every update, embedded schema migrations, and a seed tool for a non-trivial local dataset.
- **A test suite you can actually trust.** Fakes instead of a mocking framework, real concurrent goroutines (run under `-race`) for every concurrency-sensitive path, and a hard build-tag boundary between tests that need PostgreSQL and tests that don't.
- **A small, deliberate dependency footprint.** Four runtime dependencies (`pgx` for PostgreSQL, `golang.org/x/crypto` for password hashing, [`moat`](https://github.com/JonasBorgesLM/moat) for rate limiting and response security headers, [`minio-go`](https://github.com/minio/minio-go) for S3-compatible attachment storage), all pure Go — which is what lets the Docker image be a static binary on `scratch`: no shell, no libc, nothing to patch. Each one is there because something in the deployment story needs it, and `minio-go` was chosen over the AWS SDK for reasons recorded in [docs/DECISIONS.md](docs/DECISIONS.md) — notably that one client addresses MinIO in development and S3 in production, so neither environment runs a path the other never exercises.

For AI agents (or new contributors) working in this codebase, see **[CLAUDE.md](CLAUDE.md)** — architecture rules, conventions, and things not to change casually. For the full project structure, the rationale behind each design decision, and the roadmap, see **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)**. For the decisions that are easy to reverse by accident — and why — see **[docs/DECISIONS.md](docs/DECISIONS.md)**. For the full API contract, see **[docs/openapi.yaml](docs/openapi.yaml)**.

## Requirements

- **Go 1.26+**, matching `go.mod`.
- **No external service needed for the core application or the in-memory store** — the entire unit test suite runs without one. Of the four runtime dependencies, [`pgx/v5`](https://github.com/jackc/pgx) matters only once `DATABASE_URL` is configured, [`minio-go`](https://github.com/minio/minio-go) only once `ATTACHMENT_S3_ENDPOINT` is, [`golang.org/x/crypto`](https://pkg.go.dev/golang.org/x/crypto) only when a password is hashed, and [`moat`](https://github.com/JonasBorgesLM/moat) is in the request path but talks to nothing outside the process.
- **[Docker](https://www.docker.com/) and Docker Compose** (optional) — to run PostgreSQL locally without installing it directly.
- **Nothing to install for linting.** `make lint` and `make vulncheck` invoke [`staticcheck`](https://staticcheck.dev/) and [`govulncheck`](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) through `go run <pkg>@<version>`, at versions pinned in the `Makefile` (`STATICCHECK_VERSION`, `GOVULNCHECK_VERSION`). CI calls the same targets, so a local run and the pipeline cannot drift onto different linter versions.

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
| `HTTP_PRE_SHUTDOWN_DELAY` | How long to keep serving after `SIGTERM` before refusing new connections. Needed under Kubernetes, where a pod is removed from the Service and signalled concurrently — without it a zero-downtime rollout still drops requests | `0` |
| `LOG_LEVEL` | `debug`, `info`, `warn`, or `error` | `info` |
| `DOTENV_PATH` | Path to the `.env` file loaded before the OS environment | `.env` |
| `DATABASE_URL` | PostgreSQL connection string. Unset ⇒ in-memory store | *(unset)* |
| `DB_MAX_OPEN_CONNS` | Max open connections in the pool | `25` |
| `DB_MAX_IDLE_CONNS` | Max idle connections kept in the pool | `25` |
| `DB_CONN_MAX_LIFETIME` | Max lifetime of a pooled connection | `5m` |
| `DB_AUTO_MIGRATE` | Apply pending migrations automatically on startup | `true` |
| `AUTH_SESSION_TTL` | How long a `POST /v1/auth/login` token stays valid | `24h` |
| `CORS_ALLOWED_ORIGINS` | Comma-separated browser origins allowed to call this API. Unset ⇒ CORS disabled | *(unset)* |
| `HSTS_MAX_AGE` | `Strict-Transport-Security` max-age, as a Go duration (e.g. `8760h`). `0` omits the header entirely, for a permanently-plaintext deployment. Never sent with `includeSubDomains` or `preload` | `8760h` (1 year) |
| `RATE_LIMIT_BURST` / `RATE_LIMIT_PER_SEC` | Global token bucket, keyed by client address, in front of every route except the health probes | `60` / `20` |
| `AUTH_RATE_LIMIT_BURST` / `AUTH_RATE_LIMIT_PER_SEC` | Tighter bucket for `POST /v1/auth/register` and `POST /v1/auth/login` together | `10` / `0.05` |
| `USER_RATE_LIMIT_BURST` / `USER_RATE_LIMIT_PER_SEC` | Bucket keyed by authenticated user ID, on every route requiring a token | `120` / `40` |
| `TRUSTED_PROXIES` | Comma-separated CIDRs/addresses of reverse proxies you operate. Only then is `X-Forwarded-For` used to key the address-based limits — list your proxies, never your clients | *(unset)* |
| `ATTACHMENT_STORAGE_DIR` | Directory file attachments are stored under. Unset disables attachments entirely (the routes 404). Must already exist — there is no default because the `scratch` image has nowhere to write | *(unset)* |
| `ATTACHMENT_MAX_BYTES` | Largest single attachment accepted | `10485760` (10 MiB) |
| `ATTACHMENT_ORPHAN_MIN_AGE` | How long a blob must sit unreferenced before the orphan collector removes it. A safety margin against deleting uploads in flight, not a tuning knob | `1h` |
| `ATTACHMENT_S3_ENDPOINT` | Object-storage backend, as `host[:port]` without a scheme. The alternative to `ATTACHMENT_STORAGE_DIR` — setting both is rejected at startup. Required for any deployment where the process can move between machines | *(unset)* |
| `ATTACHMENT_S3_BUCKET` / `..._ACCESS_KEY` / `..._SECRET_KEY` | Required when the endpoint is set. The bucket must already exist | *(unset)* |
| `ATTACHMENT_S3_REGION` / `ATTACHMENT_S3_USE_SSL` | Optional; SSL defaults to on (turn it off for the local MinIO) | — / `true` |

`config.Load()` returns an error (and the process refuses to start) if a timeout/TTL/max-age isn't a positive Go duration, `HTTP_ADDR` isn't a valid `host:port` with a port in 1–65535, `LOG_LEVEL`/`DB_AUTO_MIGRATE` aren't one of their valid values, or a `DB_MAX_*_CONNS` isn't a positive integer. `DATABASE_URL` itself isn't format-checked — the PostgreSQL driver is the authority on what it accepts, so a bad value surfaces at connection time instead.

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

`Dockerfile` is a two-stage build: `golang:1.26.6-alpine` compiles a static (`CGO_ENABLED=0`) binary, copied into an empty `scratch` image — no shell, no libc, just the binary and a nonroot user.

```bash
make docker-build   # build the image

make docker-up       # full stack (API + PostgreSQL + Swagger UI) via docker compose
curl http://localhost:8080/health
open http://localhost:8082    # Swagger UI — browse and try every endpoint by hand
make docker-down     # stop and remove everything (data volume is kept)

make db-up            # PostgreSQL only — pair with `make run` on the host for faster edit/rebuild cycles
```

### Swagger UI

`docker-compose.yml` bundles [Swagger UI](https://github.com/swagger-api/swagger-ui) at **http://localhost:8082**, serving `docs/openapi.yaml` — mounted read-only, so editing the spec and reloading the page shows the change with no rebuild. "Try it out" works against the real `api` service with no extra setup: `CORS_ALLOWED_ORIGINS` defaults to `http://localhost:8082` specifically so the browser-based request Swagger UI's "Execute" button makes isn't blocked by CORS (see [Configuration](#configuration) and `docs/ARCHITECTURE.md`'s "Operational Behavior" section for why that's necessary — a browser enforces same-origin restrictions that a `curl` request from the same machine never hits).

Register a user via `POST /v1/auth/register`, log in via `POST /v1/auth/login` to get a token, then click **Authorize** (top right) and paste it in as `Bearer <token>` to unlock every `/v1/tasks/*` request.

Run the built image standalone against any PostgreSQL instance:

```bash
docker run --rm -p 8080:8080 \
  -e DATABASE_URL="postgres://user:pass@host:5432/dbname?sslmode=disable" \
  task-api:latest
```

## Testing

| | Unit | Integration |
|---|---|---|
| Exercises | `Service`/`Handler` (fakes), `memoryRepository`, `config`, `middleware`, a full HTTP stack over the real in-memory repositories — for both `task` and `user` | `postgresRepository` (both domains) against **real PostgreSQL** |
| External dependency | None | PostgreSQL (`TEST_DATABASE_URL`) and MinIO (`TEST_S3_ENDPOINT`) — each skipped independently if its variable is unset |
| Isolated by | Default build | `//go:build integration` on every `*/postgres_repository_test.go` — not even compiled by a plain `go test ./...` |
| Speed | Milliseconds | Needs a live database |

```bash
make test               # unit tests
make test-race           # unit tests + race detector
make coverage             # unit tests + per-function coverage report

make db-up                # start PostgreSQL
make storage-up           # start MinIO and create the bucket
make test-integration     # integration tests
make test-integration-race
make coverage-full        # unit + integration, true cross-package coverage
```

`make coverage` reports only what runs without a database, and `go test
-cover` credits coverage solely to each package's own tests — so
integration-tagged files (`postgresRepository`, `migrate`) show 0% purely
because they aren't compiled, and cross-package helpers show 0% despite
being fully exercised. `make coverage-full` measures both together
(`-tags=integration -coverpkg=./...`); that is the number worth quoting.

`internal/task/integration_test.go` (no build tag) is a full-stack HTTP test — real `Handler` → real `Service` → real in-memory `Repository`, for both `task` and `user`, including the real register/login flow — and despite the name has no external dependency; it's part of the unit suite because it validates that the layers (and both domains' auth wiring) are wired together correctly, not that PostgreSQL works. The dividing line for "integration" here is *needs a real external service*, not *spans more than one layer*.

Concurrency-sensitive paths (optimistic-concurrency conflicts) are exercised with real concurrent goroutines under `-race`, both against the in-memory store and against real PostgreSQL.

`make check` is the local gate — `gofmt`, `go mod tidy -diff`, `go vet` (default and integration tags), `staticcheck` (both), `govulncheck` and the race-tested unit suite. It is the static half of CI: the pipeline additionally runs the fuzz target, the PostgreSQL/MinIO integration suite, and a build plus smoke test of the production image.

**`govulncheck` fails the build on a vulnerability the code can actually reach.** That is its own default rather than a setting here: an advisory against something present in the dependency graph but never called exits `0`, and only a reachable one exits non-zero. The trade accepted with that choice is that an advisory against the standard library can block merges until a Go release fixes it — see `docs/DECISIONS.md`. It is worth what it costs: the four standard-library advisories this project carried before Go 1.26.6 were found by running the tool by hand, because nothing in the pipeline was looking.

## Kubernetes

`k8s/` holds manifests for a **disposable validation cluster** — PostgreSQL and MinIO run in-cluster on `emptyDir`, which is data loss anywhere that matters. A real deployment points `DATABASE_URL` and `ATTACHMENT_S3_ENDPOINT` at managed services and deletes those two files.

```bash
./k8s/rollout-test.sh          # create a kind cluster, deploy, prove the rollout, tear down
./k8s/rollout-test.sh --keep   # leave the cluster up afterwards
```

The script is the interesting part. It runs authenticated load **from inside the cluster** against the Service — `kubectl port-forward` attaches to one specific pod, so it would die exactly when that pod is replaced, and the resulting errors would be the tunnel's rather than the API's — triggers `kubectl rollout restart` mid-flight, and counts what came back. The load includes an attachment download, so a passing run also demonstrates that a session issued by the old pod still works against the new one, and that a file uploaded through one pod is readable through another.

**It found a real bug the manifests looked fine about.** With `maxUnavailable: 0`, readiness on `/health/ready` and a 30s grace period, the first run still lost 3 of 654 requests. Kubernetes removes a terminating pod from the Service and sends `SIGTERM` concurrently, and propagating that removal takes time — during the gap, traffic is still routed to a process that has already stopped listening. `HTTP_PRE_SHUTDOWN_DELAY` closes it: 0 of 732 on the next run. The usual remedy is a `preStop` hook running `sleep`, which this image cannot do — it is a static binary on `scratch`, with no shell to exec.

## Migrations

The schema lives in `internal/platform/migrate/migrations/*.{up,down}.sql`, embedded into the binary (`embed.FS`) so it needs no files deployed alongside it — shared by both the `task` and `user` domains (one `*sql.DB`, one `schema_migrations` bookkeeping table). Applied migrations are tracked in `schema_migrations`; each migration runs inside its own transaction.

- **Automatic** (default, `DB_AUTO_MIGRATE=true`): applied on API startup whenever `DATABASE_URL` is set.
- **Manual**, via the standalone `cmd/migrate` CLI — for reverting (the server only ever migrates forward) or when `DB_AUTO_MIGRATE=false` (recommended with more than one replica, so migrations run once as an explicit deploy step instead of racing on every instance's boot):

```bash
make db-up            # if not already running
make migrate-up        # apply pending migrations
make migrate-down       # revert the last applied migration
```

Both are safe to re-run: `migrate-up` with nothing pending changes nothing; `migrate-down` with nothing to revert exits cleanly.

## Seeding

`cmd/seed` populates the database with demo users and randomly generated tasks (varied titles, descriptions, statuses and priorities) for local development and manual testing against a non-trivial, multi-user dataset. Every user is created through the real `user.Service.Register`, and every task through `task.Service.CreateTask`/`TransitionStatus` — the same validation, ID generation, and timestamp logic every other write path uses — so seeded data is indistinguishable from data created through the API.

It requires PostgreSQL: seeding the in-memory store would vanish the moment the process exits, with nothing left to have observed it. It applies pending migrations itself before inserting, so it works against a freshly created, empty database with no separate `migrate-up` step.

```bash
make db-up                                             # if not already running
make seed                                               # 5 demo users, 10 tasks each (password: password123)
make seed SEED_USERS=20 SEED_TASKS_PER_USER=50          # override the defaults
make seed-reset                                         # wipe users/sessions/tasks first, then reseed
make db-reset                                           # wipe users/sessions/tasks, seed nothing
```

For direct control over the flags (`-users`, `-tasks-per-user`, `-password`, `-reset`):

```bash
DATABASE_URL="postgres://task_api:task_api@localhost:5432/task_api?sslmode=disable" \
  go run ./cmd/seed -users=10 -tasks-per-user=25
```

> **`-reset` is irreversible** — it `TRUNCATE`s `users`, `sessions` and `tasks`. It is refused outright unless `DATABASE_URL` points at `localhost`/`127.0.0.1`/`::1`, so a copy-pasted production connection string fails safely instead of emptying it. Add `-allow-remote-reset` to override that when the target really is what you meant. (`make seed-reset` / `make db-reset` point at localhost and are unaffected.)

## API

The contract lives under **`/v1`**. The health probes and `/debug/vars` are deliberately unversioned — they are operational endpoints, not part of the contract a client codes against, and the readiness probe is named in deployment manifests that shouldn't need re-editing whenever the API version moves. The unprefixed contract paths are not served at all.

All endpoints accept/return `application/json`; every response carries an `X-Request-Id` header for log correlation. Every `/v1/tasks/*` route requires `Authorization: Bearer <token>` (obtained from `POST /v1/auth/login`) and is scoped to the authenticated caller's own tasks — a task ID that exists but belongs to someone else returns `404`, identically to one that doesn't exist at all. **Full request/response schemas, validation rules, and examples: [docs/openapi.yaml](docs/openapi.yaml).**

| Method | Path | Auth | Description |
|---|---|---|---|
| `POST` | `/v1/auth/register` | — | Create a user account |
| `POST` | `/v1/auth/login` | — | Authenticate, receive a bearer session token |
| `POST` | `/v1/auth/logout` | required | Invalidate the current session token |
| `GET` | `/v1/auth/me` | required | Get the authenticated user |
| `POST` | `/v1/tasks` | required | Create a task |
| `GET` | `/v1/tasks` | required | List the caller's tasks, oldest first (`?limit=`, `?offset=`) |
| `GET` | `/v1/tasks/{id}` | required | Get a task by ID |
| `PUT` | `/v1/tasks/{id}` | required | Update title/description/priority |
| `PATCH` | `/v1/tasks/{id}/status` | required | Move a task to a new status (`pending`/`in_progress`/`done`/`cancelled`) |
| `PATCH` | `/v1/tasks/{id}/done` | required | Mark as done (idempotent shortcut for `.../status`) |
| `DELETE` | `/v1/tasks/{id}` | required | Delete a task |
| `POST` | `/v1/tasks/{id}/attachments` | required | Upload a file attachment (multipart, part name `file`) |
| `GET` | `/v1/tasks/{id}/attachments` | required | List a task's attachments |
| `GET` | `/v1/files/{key}` | required | Download an attachment by its `storage_key` |
| `GET` | `/health` | — | Liveness — always `200` while the process runs |
| `GET` | `/health/ready` | — | Readiness — `200` if the database is reachable, `503` if not |
| `GET` | `/debug/vars` | required | Runtime stats (`expvar`) plus `version`/`commit` for the running build — authenticated, unlike the health routes |

Errors always use the same envelope, `{"error": "description of the problem"}`. Common codes: `400` invalid input, `401` missing/invalid session token, `503` a dependency (the database) is unavailable — the token is fine, retry, `404` unknown or not-yours task ID, `409` optimistic-concurrency conflict or illegal status transition (re-fetch and retry), `429` a rate limit was exceeded (wait and retry; `/health` and `/health/ready` are the only routes never rate limited), `500` unexpected failure (details logged server-side, never in the response).

**Quick walkthrough:**

```bash
curl -s -X POST localhost:8080/v1/auth/register -H 'Content-Type: application/json' \
  -d '{"email":"alice@example.com","password":"correct horse battery staple"}'

TOKEN=$(curl -s -X POST localhost:8080/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"alice@example.com","password":"correct horse battery staple"}' | jq -r .token)

ID=$(curl -s -X POST localhost:8080/v1/tasks -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"title":"Buy groceries","description":"Milk, eggs, bread","priority":"high"}' | jq -r .id)

curl -s localhost:8080/v1/tasks/$ID -H "Authorization: Bearer $TOKEN"                              # → 200, the task
curl -s -X PATCH localhost:8080/v1/tasks/$ID/status -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"status":"in_progress"}'                              # → 200, status: in_progress
curl -s -o /dev/null -w '%{http_code}\n' \
  -X DELETE localhost:8080/v1/tasks/$ID -H "Authorization: Bearer $TOKEN"                           # → 204
```

## Learn more

- **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** — project structure, the reasoning behind every non-obvious design decision (session tokens, ownership model, status transitions, and more), and what's deliberately deferred.
- **[docs/DECISIONS.md](docs/DECISIONS.md)** — the decisions a future change could undo without noticing, stated as decisions rather than as descriptions: why auth is a header and not a cookie, why attachment bytes are written before their metadata row, why the content-type allow-list ignores what the client declared, and the deploy topology the rate limiter's design assumes. Read it before implementing a backlog issue; if an issue seems to contradict it, ask rather than choose.
- **[docs/openapi.yaml](docs/openapi.yaml)** — the full API contract.
- **[CLAUDE.md](CLAUDE.md)** — conventions and rules for anyone (human or agent) changing this codebase.
