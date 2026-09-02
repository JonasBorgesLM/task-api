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
| `VITE_API_BASE_URL` | Origin the app calls (e.g. `https://api.example.com`). Requests always send `credentials: 'include'` — see `src/api/client.ts` — so this must be an origin the browser will actually attach the session cookie to. | `http://localhost:8080` |
