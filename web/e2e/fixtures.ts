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

/**
 * Creates a task via the real "New task" modal and waits for it to
 * close. Deliberately does not assert the new row is visible: a task is
 * always the newest (created_at ascending order — see
 * src/features/tasks/useTasks.tsx), so once more than PAGE_SIZE tasks
 * exist it lands on the *last* page, not necessarily the one currently
 * in view. Callers on a page where the row is expected to show up
 * assert that themselves.
 */
export async function createTask(page: Page, title: string): Promise<void> {
  await page.getByRole('button', { name: 'New task' }).click()
  await page.getByLabel(/^Title/).fill(title)
  await page.getByRole('button', { name: 'Create task' }).click()
  await expect(page.getByRole('dialog', { name: 'New task' })).toHaveCount(0)
}

/**
 * Drives one status transition through TaskStatusControls' pull-down
 * menu (Fase 14 CI-12), scoped to taskTitle rather than the bare
 * `/Change status of/` pattern task-lifecycle.spec.ts uses — that's
 * only unambiguous with a single task on screen, which several of the
 * filter/pagination specs deliberately are not.
 */
export async function moveToStatus(page: Page, taskTitle: string, target: string): Promise<void> {
  const escapedTitle = taskTitle.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  await page.getByRole('button', { name: new RegExp(`Change status of "${escapedTitle}"`) }).click()
  await page.getByRole('menuitem', { name: `Move to ${target}` }).click()
}
