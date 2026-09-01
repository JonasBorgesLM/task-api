// Shared environment knobs for the e2e suite — kept separate from
// fixtures.ts (which is about the Playwright `test` object itself) so a
// spec that only needs these doesn't have to pull in the console-error
// fixture machinery too.

// Mirrors src/api/client.ts's own VITE_API_BASE_URL fallback exactly:
// the frontend build picks up the same variable (see
// playwright.config.ts's webServer command), so this is "whatever origin
// the app under test was actually built to talk to," not a second,
// possibly-divergent guess.
export const API_BASE = process.env.VITE_API_BASE_URL ?? 'http://localhost:8080'

// The `docker compose` invocation zz-resilience.spec.ts uses to take
// Postgres down and back up. Defaults to the plain, documented command
// (docker-compose.yml's own header comment); override this if your local
// stack needs extra -f/-p flags for a reason unrelated to this repo (a
// port already taken by something else on your machine, for instance) —
// see playwright.config.ts's doc comment for the full local-setup story.
export const COMPOSE_CMD = process.env.E2E_COMPOSE_CMD ?? 'docker compose'

// Chromium logs its own "Failed to load resource: the server responded
// with a status of NNN" line to the console for every non-2xx
// fetch/XHR — regardless of whether the app handled it correctly. This
// app's own error handling *depends* on triggering exactly that: the
// 401 on an unauthenticated /auth/me boot check, the 409 on a duplicate
// registration, the 503-doesn't-logout test's whole premise. Counting
// those as failures would make "zero console errors" impossible for any
// spec that exercises error handling at all, which is most of them.
// Shared between fixtures.ts's auto-fixture and zz-resilience.spec.ts's
// manual check (see its own comment for why it can't use that fixture).
export const RESOURCE_LOAD_FAILURE =
  /^Failed to load resource: the server responded with a status of/
