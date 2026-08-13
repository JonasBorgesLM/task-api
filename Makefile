.PHONY: help run build tidy clean \
        test test-race test-integration test-integration-race coverage \
        fmt fmt-check vet lint check \
        docker-build docker-up docker-down db-up \
        migrate-up migrate-down seed seed-reset

.DEFAULT_GOAL := help

# Connection string used by the local PostgreSQL instance started via
# `make db-up` / `make docker-up` (see docker-compose.yml). Override on
# the command line (e.g. `make test-integration TEST_DATABASE_URL=...`)
# to point at a different instance.
TEST_DATABASE_URL ?= postgres://task_api:task_api@localhost:5432/task_api?sslmode=disable
DATABASE_URL       ?= $(TEST_DATABASE_URL)

# Number of tasks `make seed` / `make seed-reset` create. Override on the
# command line, e.g. `make seed SEED_COUNT=200`.
SEED_COUNT ?= 20

##@ Help

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Development

run: ## Start the API (in-memory store unless DATABASE_URL is set — see README "Configuration")
	go run ./cmd/api

build: ## Compile the API binary to ./bin/task-api
	go build -o bin/task-api ./cmd/api

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

test-integration: ## Run PostgreSQL integration tests (needs `make db-up` first)
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -tags=integration ./task/... -run Postgres -v

test-integration-race: ## Run PostgreSQL integration tests with the race detector (needs `make db-up` first)
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -tags=integration -race ./task/... -run Postgres

coverage: ## Run unit tests and print a per-function coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

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
	go vet -tags=integration ./task/...

lint: ## Run staticcheck (installs it into $GOBIN if not already present)
	@command -v staticcheck >/dev/null 2>&1 || go install honnef.co/go/tools/cmd/staticcheck@latest
	staticcheck ./...
	staticcheck -tags=integration ./task/...

check: fmt-check vet lint test-race ## Run everything the CI quality gate runs (no PostgreSQL required)

##@ Docker

docker-build: ## Build the production API image (see Dockerfile)
	docker build -t task-api:latest .

docker-up: ## Start the full stack (API + PostgreSQL) via docker compose
	docker compose up -d --build

docker-down: ## Stop and remove every container docker compose started (data volume is kept)
	docker compose down

db-up: ## Start only PostgreSQL — for `make run` on the host, or `make test-integration`/`migrate-*`
	docker compose up -d postgres

##@ Database

migrate-up: ## Apply pending PostgreSQL migrations against DATABASE_URL
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/migrate -direction=up

migrate-down: ## Revert the single most recently applied migration
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/migrate -direction=down

seed: ## Populate the database with SEED_COUNT random tasks (override: `make seed SEED_COUNT=200`)
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/seed -count=$(SEED_COUNT)

seed-reset: ## Empty the tasks table, then populate it with SEED_COUNT random tasks
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/seed -count=$(SEED_COUNT) -reset
