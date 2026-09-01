import { defineConfig } from '@playwright/test'

// CI-11 of docs/changes/web-frontend/plan.md — real E2E, against the
// real docker-compose stack (`docker compose up -d --build` from the
// repo root), never a mock: a mock would only confirm what the unit
// suites (CI-3–CI-10) already assume about the contract.
//
// Before running `npx playwright test`, bring the backend up with the
// frontend's own origin allowed for CORS — the compose file's default
// (CORS_ALLOWED_ORIGINS) points at its bundled Swagger UI on :8082, not
// at this dev-server origin — and a relaxed auth-tier rate limit, since
// this whole suite legitimately makes more than the default
// AUTH_RATE_LIMIT_BURST=10 register+login calls in quick succession
// (one register+login pair per spec, several specs, one worker) — that
// default exists to slow down credential stuffing against a production
// deployment, not a single serial local test run against a fresh stack:
//
//   CORS_ALLOWED_ORIGINS=http://localhost:4173 \
//   AUTH_RATE_LIMIT_BURST=1000 AUTH_RATE_LIMIT_PER_SEC=50 \
//   docker compose up -d --build
//
// workers: 1 — not a performance default, a correctness one.
// zz-resilience.spec.ts takes the shared docker-compose Postgres
// container down mid-run (that's the whole point of the 503 test); any
// other spec executing concurrently against the same stack would fail
// for an unrelated reason. Serial execution is what makes "docker
// compose up once, run every spec against it" safe.
export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: 'list',
  use: {
    baseURL: 'http://localhost:4173',
    trace: 'retain-on-failure',
  },
  // The frontend alone — the backend is docker-compose's job (see
  // above), not something this config can honestly own, since it's a
  // whole stack (Postgres, MinIO, the bucket-creation job), not a single
  // process a webServer entry can start and health-check.
  webServer: {
    command: 'npm run build && npm run preview -- --port 4173 --strictPort',
    url: 'http://localhost:4173',
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
})
