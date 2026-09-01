import { expect, registerAndLogin, test } from './fixtures'

test('logging out redirects to /login, and the session is actually gone server-side', async ({
  page,
}) => {
  const { email } = await registerAndLogin(page)
  await expect(page.getByText(`Logged in as ${email}`)).toBeVisible()

  await page.getByRole('button', { name: 'Log out' }).click()
  await page.waitForURL(/\/login$/)

  // Not just a client-side redirect: reloading / with no session left
  // must bounce back to /login rather than briefly flashing the
  // authenticated view before a 401 catches up.
  await page.goto('/')
  await page.waitForURL(/\/login$/)
  await expect(page.getByText(`Logged in as ${email}`)).toHaveCount(0)
})
