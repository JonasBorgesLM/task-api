# task-api web frontend

SPA (Vite + React + TypeScript, `strict: true`) for [task-api](../README.md). No SSR, no server routes — see `docs/DECISIONS.md` § "Frontend: Vite (SPA), monorepo em `web/`, mesma linha de versão do repo" for why.

Versioned and released with the rest of the repo — no separate `web/` tag. See `docs/changes/web-frontend/plan.md` for the implementation plan this directory is being built against.

## Commands

Requires Node ≥22.12 (see `.nvmrc`; `nvm use` picks it up automatically).

```
npm install       # or npm ci, matching what CI runs
npm run dev        # local dev server
npm run build       # typecheck + production build
npm run typecheck    # tsc only, no build output
npm run lint       # oxlint
npm run test       # vitest, single run (CI mode)
npm run format      # prettier --write
```

`.github/workflows/web-ci.yml` runs `typecheck`, `lint`, `test` and `build` on every change under `web/**`, entirely separate from the Go gate (`.github/workflows/ci.yml`), which ignores this directory.
