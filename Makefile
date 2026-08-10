.PHONY: run build test test-race coverage test-integration \
        fmt vet lint check \
        docker-build docker-up docker-down db-up \
        migrate-up migrate-down

# Connection string used by the local PostgreSQL instance started via
# `make db-up` / `make docker-up` (see docker-compose.yml). Override on
# the command line (e.g. `make test-integration TEST_DATABASE_URL=...`)
# to point at a different instance.
TEST_DATABASE_URL ?= postgres://task_api:task_api@localhost:5432/task_api?sslmode=disable
DATABASE_URL       ?= $(TEST_DATABASE_URL)

## run: start the API (uses environment variables / .env for configuration;
## in-memory store unless DATABASE_URL is set — see README "Configuration")
run:
	go run ./cmd/api

## build: compile the API binary to ./bin/task-api
build:
	go build -o bin/task-api ./cmd/api

## test: run unit tests (memoryRepository, Service, Handler, config,
## middleware, server lifecycle — no external services required)
test:
	go test ./...

## test-race: run unit tests with the race detector enabled
test-race:
	go test -race ./...

## coverage: run unit tests and print a per-function coverage report
coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

## test-integration: run postgresRepository's integration tests against a
## real PostgreSQL instance (see `make db-up`). Built only with the
## "integration" tag, so they never run as part of `make test`.
test-integration:
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -tags=integration ./task/... -run Postgres -v

## fmt: format all Go source files
fmt:
	gofmt -w .

## vet: run go vet (unit-tagged and integration-tagged source)
vet:
	go vet ./...
	go vet -tags=integration ./task/...

## lint: run staticcheck (installs it into $GOBIN if not already present)
lint:
	@command -v staticcheck >/dev/null 2>&1 || go install honnef.co/go/tools/cmd/staticcheck@latest
	staticcheck ./...

## check: everything the CI quality gate runs, in one command
check: fmt vet lint test-race

## docker-build: build the production API image (see Dockerfile)
docker-build:
	docker build -t task-api:latest .

## docker-up: start the full stack (API + PostgreSQL) via docker compose
docker-up:
	docker compose up -d --build

## docker-down: stop and remove every container docker compose started
## (whether started via docker-up or db-up below); the data volume is kept
docker-down:
	docker compose down

## db-up: start only PostgreSQL via docker compose — for running the API
## with `make run` on the host (faster edit/rebuild loop than docker-up)
## or for `make test-integration` / `make migrate-up` / `make migrate-down`
db-up:
	docker compose up -d postgres

## migrate-up: apply pending PostgreSQL migrations against DATABASE_URL
## (defaults to the local docker-compose instance; override to target
## another database, e.g. `make migrate-up DATABASE_URL=...`)
migrate-up:
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/migrate -direction=up

## migrate-down: revert the single most recently applied migration
migrate-down:
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/migrate -direction=down
