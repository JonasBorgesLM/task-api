.PHONY: help run build tidy clean \
        test test-race test-integration test-integration-race coverage coverage-full fuzz \
        fmt fmt-check vet lint vulncheck tidy-check check \
        docker-build docker-up docker-down db-up storage-up \
        migrate-up migrate-down seed seed-reset db-reset \
        signoz-dashboard \
        changelog-section

.DEFAULT_GOAL := help

# Versões pinadas das ferramentas de análise. Elas são invocadas via
# `go run <pkg>@<versão>`, não instaladas no $GOBIN: com `go install` +
# `command -v`, uma versão já presente na máquina é usada em silêncio no
# lugar da que o projeto escolheu — o que fazia `make lint` local e o CI
# rodarem linters diferentes sem nada indicar isso. `go run` resolve a
# versão exata e usa o cache de módulos a partir da primeira execução.
#
# Atualizar é deliberado: suba o número aqui, rode `make check`, e o
# commit registra a troca. O CI chama estes mesmos alvos, então não há um
# segundo lugar para desalinhar.
STATICCHECK_VERSION ?= v0.8.1
GOVULNCHECK_VERSION ?= v1.7.0

# Connection string used by the local PostgreSQL instance started via
# `make db-up` / `make docker-up` (see docker-compose.yml). Override on
# the command line (e.g. `make test-integration TEST_DATABASE_URL=...`)
# to point at a different instance.
TEST_DATABASE_URL ?= postgres://task_api:task_api@localhost:5432/task_api?sslmode=disable
DATABASE_URL       ?= $(TEST_DATABASE_URL)

# Demo users and tasks-per-user `make seed` / `make seed-reset` create.
# Override on the command line, e.g. `make seed SEED_USERS=20 SEED_TASKS_PER_USER=50`.
SEED_USERS ?= 5
SEED_TASKS_PER_USER ?= 10

# What `make build` and `make docker-build` stamp into the binary via
# -ldflags -X (see cmd/api/main.go's version/commit doc comment for why
# this exists at all — it's the only way a running pod can be traced
# back to the exact commit it was built from). Both fall back cleanly
# when git isn't available or there's no history to describe (a shallow
# clone, a tarball export): "dev"/"unknown", the same defaults main.go
# itself uses for a build with no -ldflags.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

# How long `make fuzz` runs for. CI uses its own budget (see
# .github/workflows/ci.yml); a longer one here is what you want when
# deliberately hunting rather than guarding against regression.
FUZZTIME ?= 45s

# MinIO started by `make storage-up` / `make docker-up` (see
# docker-compose.yml), used by internal/attachment's S3 integration
# tests. Unset TEST_S3_ENDPOINT to skip them rather than fail.
TEST_S3_ENDPOINT   ?= localhost:9000
TEST_S3_ACCESS_KEY ?= task_api
TEST_S3_SECRET_KEY ?= task_api_secret

##@ Help

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Development

run: ## Start the API (in-memory store unless DATABASE_URL is set — see README "Configuration")
	go run ./cmd/api

build: ## Compile the API binary to ./bin/task-api (stamped with VERSION/COMMIT)
	go build -ldflags="-X main.version=$(VERSION) -X main.commit=$(COMMIT)" -o bin/task-api ./cmd/api

tidy: ## Tidy and verify go.mod/go.sum
	go mod tidy
	go mod verify

clean: ## Remove build artifacts (bin/, coverage.out)
	rm -rf bin coverage.out

##@ Testing

test: ## Run unit tests (no external services required)
	go test ./...

test-race: ## Run unit tests with the race detector enabled
	go test -race ./...

# -p 1 forces packages to be tested sequentially rather than `go test`'s
# default of running each package's tests in its own concurrent process:
# every integration-tagged package here shares one real PostgreSQL
# instance/schema (see TEST_DATABASE_URL), and internal/platform/migrate's
# tests in particular DROP TABLE the schema other packages' tests assume
# is already migrated and stable — running two packages' integration
# suites concurrently against the same database is a real, observed
# source of spurious failures, not just a theoretical race.
#
# There is deliberately no `-run Postgres` filter: the `integration` build
# tag already decides what belongs here, and selecting by name on top of
# that meant any integration test not named TestPostgres_* was silently
# skipped — passing CI without ever executing. Unit tests in the same
# packages run too, which costs a few seconds and removes a way to be
# wrong.
test-integration: ## Run integration tests — PostgreSQL + MinIO (needs `make db-up storage-up` first)
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" \
	TEST_S3_ENDPOINT="$(TEST_S3_ENDPOINT)" \
	TEST_S3_ACCESS_KEY="$(TEST_S3_ACCESS_KEY)" \
	TEST_S3_SECRET_KEY="$(TEST_S3_SECRET_KEY)" \
	go test -p 1 -tags=integration ./... -v

test-integration-race: ## Run integration tests with the race detector (needs `make db-up storage-up` first)
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" \
	TEST_S3_ENDPOINT="$(TEST_S3_ENDPOINT)" \
	TEST_S3_ACCESS_KEY="$(TEST_S3_ACCESS_KEY)" \
	TEST_S3_SECRET_KEY="$(TEST_S3_SECRET_KEY)" \
	go test -p 1 -tags=integration -race ./...

fuzz: ## Fuzz the attachment store's path containment (override: `make fuzz FUZZTIME=5m`)
	go test ./internal/attachment/ -run FuzzFSBlobStore_OpenNeverEscapesRoot -fuzz FuzzFSBlobStore_OpenNeverEscapesRoot -fuzztime $(FUZZTIME)

coverage: ## Run unit tests and print a per-function coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

# The plain `coverage` target above measures only what runs without a
# database, and `go test -cover` attributes coverage solely to each
# package's own tests. Both understate this codebase substantially: the
# integration-tagged postgresRepository/migrate files report 0% simply
# because they aren't compiled, and helpers exercised across package
# boundaries (internal/middleware's context plumbing, driven by
# internal/user and internal/task) report 0% despite being fully covered.
# -tags=integration plus -coverpkg=./... measures what is actually tested.
coverage-full: ## Run unit + integration tests and report true cross-package coverage (needs `make db-up` first)
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -p 1 -tags=integration \
		-coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

##@ Code Quality

fmt: ## Format all Go source files in place
	gofmt -w .

fmt-check: ## Fail if any Go source file is not gofmt-formatted (matches CI)
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "The following files are not gofmt-formatted:"; echo "$$unformatted"; exit 1; \
	fi

vet: ## Run go vet (default-tagged and integration-tagged source)
	go vet ./...
	go vet -tags=integration ./...

lint: ## Run staticcheck at the pinned version (see STATICCHECK_VERSION)
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) -tags=integration ./...

vulncheck: ## Run govulncheck at the pinned version (see GOVULNCHECK_VERSION)
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

tidy-check: ## Fail if go.mod/go.sum are not tidy (matches CI; fix with `make tidy`)
	@if ! go mod tidy -diff; then \
		echo ""; \
		echo "go.mod/go.sum não estão tidy. Rode: make tidy"; \
		exit 1; \
	fi
	go mod verify

check: fmt-check tidy-check vet lint vulncheck test-race ## Run the CI static gate + race-tested unit tests (no PostgreSQL/MinIO; CI also runs fuzz and the integration suite)

##@ Docker

docker-build: ## Build the production API image (see Dockerfile), stamped with VERSION/COMMIT
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t task-api:latest .

docker-up: ## Start the full stack (API + PostgreSQL + Swagger UI at :8082) via docker compose
	VERSION=$(VERSION) COMMIT=$(COMMIT) docker compose up -d --build

docker-down: ## Stop and remove every container docker compose started (data volume is kept)
	docker compose down

storage-up: ## Start only MinIO (+ create the bucket) — for `make test-integration`
	docker compose up -d minio minio-bucket

db-up: ## Start only PostgreSQL — for `make run` on the host, or `make test-integration`/`migrate-*`
	docker compose up -d postgres

##@ Database

migrate-up: ## Apply pending PostgreSQL migrations against DATABASE_URL
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/migrate -direction=up

migrate-down: ## Revert the single most recently applied migration
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/migrate -direction=down

seed: ## Populate the database with SEED_USERS demo users and SEED_TASKS_PER_USER tasks each
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/seed -users=$(SEED_USERS) -tasks-per-user=$(SEED_TASKS_PER_USER)

seed-reset: ## Empty users/sessions/tasks, then reseed with SEED_USERS/SEED_TASKS_PER_USER
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/seed -users=$(SEED_USERS) -tasks-per-user=$(SEED_TASKS_PER_USER) -reset

db-reset: ## Wipe ALL data (users, sessions, tasks) without reseeding
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/seed -reset -users=0

##@ Observability

# Where the local SigNoz instance's API lives (see docs/DECISIONS.md's Fase
# 11 section — it runs via Docker Compose/Foundry, outside this repo).
SIGNOZ ?= http://localhost:8080

# The crier commit the dashboard template below is fetched from. Pinned to
# the exact commit it was captured and verified against (SigNoz v0.139.0,
# see crier's docs/observability/signoz/README.md), not "main" — the
# template is documentation, not a tested contract (ADR-0023 in crier), so
# nothing here should change out from under this target until someone
# deliberately bumps this on purpose, the same policy STATICCHECK_VERSION
# and GOVULNCHECK_VERSION follow above.
CRIER_DASHBOARD_REF ?= 87048ec40ef3d3e2a47a32be0c47f4881cd100f3

signoz-dashboard: ## Provision crier's task-api SigNoz dashboard (needs SIGNOZ_API_KEY_FILE=<path to a SigNoz API key from its UI>; SIGNOZ defaults to localhost:8080)
	@test -n "$(SIGNOZ_API_KEY_FILE)" || { echo "SIGNOZ_API_KEY_FILE is required: a file holding a SigNoz API key minted in its UI, e.g. make signoz-dashboard SIGNOZ_API_KEY_FILE=~/.signoz-pat" >&2; exit 1; }
	curl -fsSL "https://raw.githubusercontent.com/JonasBorgesLM/crier/$(CRIER_DASHBOARD_REF)/docs/observability/signoz/dashboard.json" \
		| sed "s/{{\.ServiceName}}/task-api/g" \
		| curl -sS -w '\nHTTP %{http_code}\n' -X POST "$(SIGNOZ)/api/v2/dashboards" \
			-H "SIGNOZ-API-KEY: $$(cat $(SIGNOZ_API_KEY_FILE))" \
			-H 'Content-Type: application/json' \
			--data-binary @-

##@ Release

changelog-section: ## Print one version's section from CHANGELOG.md (e.g. make changelog-section VERSION=1.1.0)
	@# CHANGELOG.md holds every release, newest section first, each
	@# headed `## [x.y.z] — ...`. Once a second entry existed alongside
	@# [1.0.0], `gh release create --notes-file CHANGELOG.md` stopped
	@# being correct — it publishes the whole file, every version's notes
	@# concatenated, as this one release's notes. This target is the fix:
	@# it prints just the one section asked for, header included, stopping
	@# at the next `## [` line or end of file, with the `---` divider that
	@# separates it from the next section (and any blank lines around it)
	@# trimmed off the end — buffered in an array rather than printed as
	@# each line is read, since that trailing divider isn't known to be
	@# trailing until the line *after* it (the next section's own `## [`,
	@# or EOF) is reached. Reuses the VERSION variable already defined
	@# above (for build stamping) — pass it explicitly here since a
	@# release's version is rarely the same as HEAD's `git describe`.
	@test -n "$(VERSION)" || { echo "usage: make changelog-section VERSION=1.1.0" >&2; exit 1; }
	@awk -v ver="$(VERSION)" ' \
		BEGIN { target = "## [" ver "]"; found = 0; n = 0 } \
		/^## \[/ { \
			if (found) exit; \
			if (index($$0, target) == 1) { found = 1; buf[++n] = $$0; next } \
			next \
		} \
		found { buf[++n] = $$0 } \
		END { \
			if (!found) { print "changelog-section: no \"" target "\" heading in CHANGELOG.md" > "/dev/stderr"; exit 1 } \
			while (n > 0 && (buf[n] == "" || buf[n] == "---")) { n-- } \
			for (i = 1; i <= n; i++) { print buf[i] } \
		} \
	' CHANGELOG.md
