import { test as base, expect, type Page } from '@playwright/test'
import { RESOURCE_LOAD_FAILURE } from './env'

// Extends Playwright's own `test` with an auto-fixture that fails a spec
// if the page logs a console error at any point during it — issue #129's
// "zero erro de console durante os fluxos" criterion, checked explicitly
// rather than inferred from the assertions passing (a component can
// throw a caught-and-swallowed error, or React can log a key/prop
// warning, without failing a single expect()). See env.ts's
// RESOURCE_LOAD_FAILURE for what's deliberately excluded and why.

export const test = base.extend<{ forEachTest: void }>({
  forEachTest: [
    async ({ page }, use) => {
      const errors: string[] = []
      page.on('console', (msg) => {
        if (msg.type() === 'error' && !RESOURCE_LOAD_FAILURE.test(msg.text())) {
          errors.push(msg.text())
        }
      })
      page.on('pageerror', (err) => errors.push(err.message))

      await use()

      expect(errors, `console errors during the test:\n${errors.join('\n')}`).toEqual([])
    },
    { auto: true },
  ],
})

export { expect }

/**
 * Registers a fresh account and logs in through the real UI (no API
 * shortcut) — every spec needs an authenticated session, and going
 * through the actual register+login screens each time is itself part of
 * what CI-11 is verifying, not overhead to avoid.
 */
export async function registerAndLogin(page: Page): Promise<{ email: string; password: string }> {
  const email = `e2e-${Date.now()}-${Math.random().toString(36).slice(2)}@example.com`
  const password = 'correct horse battery staple'

  await page.goto('/register')
  await page.getByLabel(/^Email/).fill(email)
  await page.getByLabel(/^Password/).fill(password)
  await page.getByRole('button', { name: 'Create account' }).click()
  await page.waitForURL(/\/login$/)

  await page.getByLabel(/^Email/).fill(email)
  await page.getByLabel(/^Password/).fill(password)
  await page.getByRole('button', { name: 'Log in' }).click()
  await page.waitForURL('/')

  return { email, password }
}
