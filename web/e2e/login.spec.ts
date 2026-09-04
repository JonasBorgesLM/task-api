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
  // AppShell's user menu shows the bare email, not "Logged in as
  // {email}" — that prefix was deliberately dropped in Fase 14's CI-4
  // (see App.tsx's own doc comment): the shell already establishes
  // "this is your account" through the menu's position/context, so
  // announcing it a second time in the text itself would be redundant.
  await expect(page.getByText(email)).toBeVisible()
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
