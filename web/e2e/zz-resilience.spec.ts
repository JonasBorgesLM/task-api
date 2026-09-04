import { execSync } from 'node:child_process'
import { expect, test, type Page } from '@playwright/test'
import { API_BASE, COMPOSE_CMD, RESOURCE_LOAD_FAILURE } from './env'
import { registerAndLogin } from './fixtures'

// This file manages its own Page across a serial run (see below) instead
// of using fixtures.ts's extended `test` — that wrapper's console-error
// auto-fixture is keyed to Playwright's own per-test `page` fixture,
// which isn't the one actually used here. Zero-console-errors is
// checked the same way, just by hand: collected in beforeAll, asserted
// in afterAll.
const consoleErrors: string[] = []

// cwd: '..' — this spec runs from web/, docker-compose.yml lives at the
// repo root.
function stopPostgres() {
  execSync(`${COMPOSE_CMD} stop postgres`, { cwd: '..' })
}

function startPostgres() {
  execSync(`${COMPOSE_CMD} start postgres`, { cwd: '..' })
}

async function waitUntilReady() {
  for (let i = 0; i < 30; i++) {
    try {
      const res = await fetch(`${API_BASE}/health/ready`)
      if (res.ok) return
    } catch {
      // ignore, retry
    }
    await new Promise((r) => setTimeout(r, 1000))
  }
  throw new Error('postgres did not become ready again in time')
}

test.describe.configure({ mode: 'serial' })

// One shared page/session across every test in this file, created
// *before* the outage starts — registering or logging in a fresh user
// while Postgres is down can't work (both need the database), so the
// mid-outage tests below have to reuse a session that was already
// established while things were healthy, exactly like a real user whose
// browser tab was already open when the outage began.
let page: Page

test.beforeAll(async ({ browser }) => {
  page = await browser.newPage()
  page.on('console', (msg) => {
    if (msg.type() === 'error' && !RESOURCE_LOAD_FAILURE.test(msg.text())) {
      consoleErrors.push(msg.text())
    }
  })
  page.on('pageerror', (err) => consoleErrors.push(err.message))
})

test.afterAll(async () => {
  await page.close()
  // Unconditional: whatever happened above, the shared docker-compose
  // stack must not be left down for whatever runs next (another spec
  // file re-run, a developer poking at it by hand).
  startPostgres()
  await waitUntilReady()

  expect(
    consoleErrors,
    `console errors during zz-resilience.spec.ts:\n${consoleErrors.join('\n')}`,
  ).toEqual([])
})

test('a 503 from a mid-session action does not clear the session', async () => {
  await registerAndLogin(page)
  await page.getByRole('button', { name: 'New task' }).click()
  await page.getByLabel(/^Title/).fill('Task before outage')
  await page.getByRole('button', { name: 'Create task' }).click()
  await expect(page.getByRole('heading', { name: 'Task before outage', level: 3 })).toBeVisible()

  stopPostgres()

  // A mutating request made while already authenticated — the session
  // itself was validated at login, before the outage; this exercises
  // RequireAuth's mid-request DB failure on the *next* call.
  await page.getByRole('button', { name: /Change status of/ }).click()
  await page.getByRole('menuitem', { name: 'Move to In progress' }).click()
  await expect(page.getByRole('alert')).toBeVisible({ timeout: 10000 })

  // Still authenticated — no redirect, session state untouched.
  await expect(page).toHaveURL('/')
  await expect(page.getByRole('button', { name: 'Log out' })).toBeVisible()
})

test('reloading mid-outage does not bounce an authenticated user to /login', async () => {
  // Postgres is still down from the previous test — the whole point.
  await page.reload()

  // The real failure mode this guards against: a naive "any non-2xx
  // from /auth/me means logged out" boot check would redirect here.
  // A stuck loading state is the correct, honest alternative — this app
  // genuinely cannot tell, over an httpOnly cookie, whether the session
  // is fine and only the reachability check failed.
  await page.waitForTimeout(1000)
  await expect(page).toHaveURL('/')
  await expect(page.getByRole('heading', { name: 'Log in' })).toHaveCount(0)
})

test('once postgres recovers, a page reload restores the authenticated view', async () => {
  startPostgres()
  await waitUntilReady()

  await page.reload()
  await page.waitForURL('/')
  await expect(page.getByRole('button', { name: 'Log out' })).toBeVisible()
})
