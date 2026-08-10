# syntax=docker/dockerfile:1

## ---------------------------------------------------------------------
## Build stage
##
## Uses the official Go image (matching the version pinned in go.mod) to
## compile a statically linked binary. Nothing from this stage — the Go
## toolchain, module cache, or source tree — is present in the final
## image; only the compiled binary is copied out of it below.
## ---------------------------------------------------------------------
FROM golang:1.26.5-alpine AS builder

WORKDIR /src

# Copy just the module files first so `go mod download` is cached in its
# own layer and only re-runs when dependencies actually change, not on
# every source edit.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 removes the libc dependency, producing a fully static
# binary — this is what makes it possible to run on the empty `scratch`
# base below instead of needing glibc/musl in the final image.
# -trimpath strips local build-machine file paths from the binary.
# -ldflags="-s -w" strips the symbol table and DWARF debug info: this is
# a release artifact, not something meant to be attached to with delve.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/task-api \
      ./cmd/api

## ---------------------------------------------------------------------
## Runtime stage
##
## `scratch` is literally empty — no shell, no package manager, no libc,
## no CA certificates. `pgx` (see go.mod) implements the PostgreSQL wire
## protocol in pure Go, so connecting to PostgreSQL over plain TCP or
## sslmode=require needs nothing from the image beyond the binary itself.
## sslmode=verify-full (which validates the server's certificate against
## a CA) is the one case this image cannot support as-is — it would need
## a CA bundle added via `COPY --from=builder /etc/ssl/certs/... `.
## ---------------------------------------------------------------------
FROM scratch

# UID/GID 65532 is the conventional unprivileged "nonroot" identity also
# used by Google's distroless images. No /etc/passwd entry is required
# for a container to run as a given numeric UID/GID — the kernel only
# needs the number — which matters here since `scratch` has no tooling
# available to create one.
USER 65532:65532

COPY --from=builder /out/task-api /task-api

# Documents the port the application listens on by default (HTTP_ADDR
# defaults to ":8080" — see config/config.go). This is metadata only; it
# does not publish the port. If HTTP_ADDR is overridden at `docker run`
# time, publish the matching port instead (see .env.example for the full
# list of supported environment variables — HTTP_ADDR, HTTP_READ_TIMEOUT,
# HTTP_WRITE_TIMEOUT, HTTP_IDLE_TIMEOUT, HTTP_SHUTDOWN_TIMEOUT, LOG_LEVEL,
# DOTENV_PATH, DATABASE_URL and the DB_* pool/migration settings).
EXPOSE 8080

# Exec form is required (not `ENTRYPOINT /task-api`, the shell form) so
# the compiled binary itself runs as PID 1 and receives SIGTERM directly
# from the container runtime — no shell in between to swallow it. main()
# already handles SIGINT/SIGTERM via signal.NotifyContext and drains
# in-flight requests through http.Server.Shutdown, so no extra init
# process (tini, dumb-init, ...) is needed to forward signals or reap
# children — this binary never forks any.
ENTRYPOINT ["/task-api"]
