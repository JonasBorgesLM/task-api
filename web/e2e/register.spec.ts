import { expect, test } from './fixtures'

test('registering a new account lands on /login, ready to sign in', async ({ page }) => {
  const email = `e2e-register-${Date.now()}@example.com`
  const password = 'correct horse battery staple'

  await page.goto('/register')
  await page.getByLabel(/^Email/).fill(email)
  await page.getByLabel(/^Password/).fill(password)
  await page.getByRole('button', { name: 'Create account' }).click()

  await page.waitForURL(/\/login$/)
  await expect(page.getByText('Account created — log in below.')).toBeVisible()
})

test("registering with an already-used email shows the server's conflict message", async ({
  page,
}) => {
  const email = `e2e-dup-${Date.now()}@example.com`
  const password = 'correct horse battery staple'

  async function register() {
    await page.goto('/register')
    await page.getByLabel(/^Email/).fill(email)
    await page.getByLabel(/^Password/).fill(password)
    await page.getByRole('button', { name: 'Create account' }).click()
  }

  await register()
  await page.waitForURL(/\/login$/)

  await register()
  await expect(page.getByRole('alert')).toContainText(/already registered/i)
  await expect(page).toHaveURL(/\/register$/)
})
