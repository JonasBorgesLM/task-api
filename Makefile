.PHONY: run test test-race test-postgres fmt vet check db-up db-down

TEST_DATABASE_URL ?= postgres://task_api:task_api@localhost:5432/task_api?sslmode=disable

## run: start the server (uses environment variables for configuration)
run:
	go run ./cmd/api

## test: run all tests (PostgreSQL tests are skipped unless TEST_DATABASE_URL is set)
test:
	go test ./...

## test-race: run all tests with the race detector enabled
test-race:
	go test -race ./...

## test-postgres: run postgresRepository's integration tests against docker-compose's Postgres
test-postgres:
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test ./task/... -run Postgres -v

## fmt: format all Go source files
fmt:
	gofmt -w .

## vet: run static analysis
vet:
	go vet ./...

## check: format, vet, and test with race detector
check: fmt vet test-race

## db-up: start a local PostgreSQL instance via docker compose
db-up:
	docker compose up -d postgres

## db-down: stop and remove the local PostgreSQL instance (keeps its data volume)
db-down:
	docker compose down
