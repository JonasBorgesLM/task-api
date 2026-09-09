# syntax=docker/dockerfile:1
#
# Built from the REPOSITORY ROOT as build context, not from web/ — see
# docker-compose.yml's `web` service (`context: ., dockerfile:
# web/Dockerfile`). cmd/web (the Go static server this image runs) lives
# in the same Go module as cmd/api, at the repository root; a
# web/-scoped context could never reach it.

## ---------------------------------------------------------------------
## Stage 1: build the SPA
##
## Node only exists in this stage. VITE_API_BASE_URL is baked into the
## JS bundle at this point (Vite reads import.meta.env.VITE_* from the
## process environment during `vite build`, not at container runtime) —
## it MUST name the same origin cmd/web's own WEB_API_ORIGIN allows in
## its CSP connect-src, or the built app's own fetch calls to its API
## are blocked by the very policy this image sends. See
## docs/DECISIONS.md § "SPA deployment: um servidor Go próprio" for the
## full reasoning on why these two are two settings, not one.
## ---------------------------------------------------------------------
FROM node:22-alpine AS node-builder

WORKDIR /src/web

# package.json/package-lock.json copied alone first so `npm ci` is
# cached in its own layer, same reasoning as the Go builder stage below
# caching `go mod download` — only re-runs when dependencies change, not
# on every source edit.
COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ .

ARG VITE_API_BASE_URL
ENV VITE_API_BASE_URL=${VITE_API_BASE_URL}
RUN npm run build

## ---------------------------------------------------------------------
## Stage 2: build cmd/web
##
## Same shape as the repository root Dockerfile's builder stage — see
## that file's own comments for CGO_ENABLED=0/-trimpath/-ldflags, which
## apply identically here. VERSION/COMMIT are not threaded through: this
## binary has no /debug/vars-equivalent build-info endpoint, because
## unlike cmd/api it holds no state and answers no request whose
## correctness could depend on which commit is running — a static file
## server either serves the files in /dist or it does not, and that is
## already visible from what index.html itself renders.
## ---------------------------------------------------------------------
FROM golang:1.26.6-alpine AS go-builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/web \
      ./cmd/web

## ---------------------------------------------------------------------
## Runtime stage
##
## scratch, same reasoning as the root Dockerfile: this binary is fully
## static (CGO_ENABLED=0) and serves nothing that needs a shell, a
## package manager, or libc. Unlike cmd/api it makes no outbound TLS
## connection of its own (no database, no S3), so it needs no CA bundle
## either.
## ---------------------------------------------------------------------
FROM scratch

# Same conventional unprivileged UID/GID as the root Dockerfile — see
# that file's comment for why no /etc/passwd entry is needed.
USER 65532:65532

COPY --from=go-builder /out/web /web
COPY --from=node-builder /src/web/dist /dist

EXPOSE 8080

ENTRYPOINT ["/web"]
