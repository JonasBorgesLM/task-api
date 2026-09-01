import { expect, test } from './fixtures'

async function register(page: import('@playwright/test').Page, email: string, password: string) {
  await page.goto('/register')
  await page.getByLabel(/^Email/).fill(email)
  await page.getByLabel(/^Password/).fill(password)
  await page.getByRole('button', { name: 'Create account' }).click()
  await page.waitForURL(/\/login$/)
}

test('logging in with the right credentials reaches the authenticated task list', async ({
  page,
}) => {
  const email = `e2e-login-${Date.now()}@example.com`
  const password = 'correct horse battery staple'
  await register(page, email, password)

  await page.getByLabel(/^Email/).fill(email)
  await page.getByLabel(/^Password/).fill(password)
  await page.getByRole('button', { name: 'Log in' }).click()

  await page.waitForURL('/')
  await expect(page.getByText(`Logged in as ${email}`)).toBeVisible()
})

test('a wrong password is rejected with the same message as an unknown email (anti-enumeration)', async ({
  page,
}) => {
  const email = `e2e-badlogin-${Date.now()}@example.com`
  const password = 'correct horse battery staple'
  await register(page, email, password)

  await page.getByLabel(/^Email/).fill(email)
  await page.getByLabel(/^Password/).fill('wrong password entirely')
  await page.getByRole('button', { name: 'Log in' }).click()
  const wrongPasswordMessage = await page.getByRole('alert').textContent()

  await page.reload()
  await page.getByLabel(/^Email/).fill(`nobody-${Date.now()}@example.com`)
  await page.getByLabel(/^Password/).fill('does not matter')
  await page.getByRole('button', { name: 'Log in' }).click()
  const unknownEmailMessage = await page.getByRole('alert').textContent()

  expect(wrongPasswordMessage).toBe(unknownEmailMessage)
  await expect(page).toHaveURL(/\/login$/)
})
