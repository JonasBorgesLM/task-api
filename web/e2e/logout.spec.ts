import { expect, registerAndLogin, test } from './fixtures'

test('logging out redirects to /login, and the session is actually gone server-side', async ({
  page,
}) => {
  const { email } = await registerAndLogin(page)
  // AppShell's user menu shows the bare email, not "Logged in as
  // {email}" — see login.spec.ts's own comment on the same change
  // (Fase 14 CI-4, App.tsx's doc comment).
  await expect(page.getByText(email)).toBeVisible()

  await page.getByRole('button', { name: 'Log out' }).click()
  await page.waitForURL(/\/login$/)

  // Not just a client-side redirect: reloading / with no session left
  // must bounce back to /login rather than briefly flashing the
  // authenticated view before a 401 catches up.
  await page.goto('/')
  await page.waitForURL(/\/login$/)
  await expect(page.getByText(email)).toHaveCount(0)
})
