.PHONY: run test test-race fmt vet check

## run: start the server (uses environment variables for configuration)
run:
	go run .

## test: run all tests
test:
	go test ./...

## test-race: run all tests with the race detector enabled
test-race:
	go test -race ./...

## fmt: format all Go source files
fmt:
	gofmt -w .

## vet: run static analysis
vet:
	go vet ./...

## check: format, vet, and test with race detector
check: fmt vet test-race
