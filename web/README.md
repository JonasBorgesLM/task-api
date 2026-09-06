# task-api web frontend

SPA (Vite + React + TypeScript, `strict: true`) for [task-api](../README.md). No SSR, no server routes — see `docs/DECISIONS.md` § "Frontend: Vite (SPA), monorepo em `web/`, mesma linha de versão do repo" for why.

Versioned and released with the rest of the repo — no separate `web/` tag. See `docs/changes/web-frontend/plan.md` for the implementation plan this directory was built against.

## Commands

Requires Node ≥22.12 (see `.nvmrc`; `nvm use` picks it up automatically).

```
npm install            # or npm ci, matching what CI runs
npm run dev            # local dev server
npm run build          # typecheck + production build
npm run typecheck      # tsc only, no build output
npm run lint           # oxlint
npm run test           # vitest, single run (CI mode)
npm run test:e2e       # playwright, against a real docker-compose stack — see playwright.config.ts
npm run format         # prettier --write
npm run generate:types # regenerate src/api/types.ts from docs/openapi.yaml — never hand-edit that file
```

`.github/workflows/web-ci.yml` runs `typecheck`, `lint`, a check that nothing outside `src/api/` calls `fetch` directly, `test` and `build` on every change under `web/**`, entirely separate from the Go gate (`.github/workflows/ci.yml`), which ignores this directory. `test:e2e` is not part of that gate — it needs the real API running (`docker compose up` from the repo root), not just Node; run it locally following `playwright.config.ts`'s own doc comment (which also covers the CORS and rate-limit env overrides a local run needs).

## Configuration

| Variable | Description | Default |
|---|---|---|
| `VITE_API_BASE_URL` | Origin the app calls (e.g. `https://api.example.com`). Requests always send `credentials: 'include'` — see `src/api/client.ts` — so this must be an origin the browser will actually attach the session cookie to. Baked into the built bundle at `npm run build` time, not read at runtime. | `http://localhost:8080` |

## Deployment

`npm run build`'s output (`dist/`) is served in production by `cmd/web` — a small Go static-file server, its own binary and its own image (`web/Dockerfile`), separate from the API's. Not nginx or Caddy: see `docs/DECISIONS.md` § "SPA deployment: um servidor Go próprio" for why, and issue #229 for the gap it closes (before it, this frontend's production build had no deploy path and no security headers of its own — `cmd/api`'s strict CSP protects the API's origin, which serves no HTML and was never the one an XSS would run from).

`cmd/web` has its own environment variables — read directly with `os.Getenv`, not through `.env` (it has no dotenv support; that is `internal/config`'s, for `cmd/api` only):

| Variable | Description | Default |
|---|---|---|
| `WEB_API_ORIGIN` | **Required.** The origin this server's `Content-Security-Policy: connect-src` allows, in addition to `'self'`. Must name the exact same origin as `VITE_API_BASE_URL` baked into this build — a mismatch doesn't fail either process at startup, it silently blocks the app's own `fetch` calls in the browser. | *(none)* |
| `WEB_ADDR` | TCP address the server listens on. | `:8080` |
| `WEB_DIST_DIR` | Directory to serve — `web/Dockerfile` copies the build here. | `/dist` |
| `WEB_HSTS_MAX_AGE` | `Strict-Transport-Security` max-age, as a Go duration. `0` omits the header. | `8760h` (365 days) |
| `WEB_SHUTDOWN_TIMEOUT` | Graceful shutdown budget after a signal. | `10s` |
| `WEB_PRE_SHUTDOWN_DELAY` | Keep serving this long after SIGTERM before refusing new connections — the same rolling-update drain `cmd/api`'s `HTTP_PRE_SHUTDOWN_DELAY` exists for. `0` (the default) is correct for `docker compose`; a Kubernetes Deployment behind a Service needs it set (see `k8s/50-web.yaml`). | `0` |

The Content-Security-Policy this server sends is not a fixed string: it hashes `dist/index.html`'s own inline theme-preference script at startup and includes that `sha256-...` in `script-src`, so the policy is always correct for whatever this process actually serves — see `cmd/web/main.go`'s `buildCSP`/`cspScriptHashes` doc comments.

`docker compose up web` runs it locally against the compose stack's API (see `docker-compose.yml`'s `web` service for the exact wiring, including why `CORS_ALLOWED_ORIGINS` needs the frontend's origin added). `k8s/50-web.yaml` is the disposable-cluster equivalent.
