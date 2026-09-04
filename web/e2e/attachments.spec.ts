import { readFileSync } from 'node:fs'
import { API_BASE } from './env'
import { expect, registerAndLogin, test } from './fixtures'

test('uploading a file and downloading it back returns the exact same bytes', async ({ page }) => {
  await registerAndLogin(page)

  await page.getByRole('button', { name: 'New task' }).click()
  await page.getByLabel(/^Title/).fill('Task with an attachment')
  await page.getByRole('button', { name: 'Create task' }).click()
  await expect(
    page.getByRole('heading', { name: 'Task with an attachment', level: 3 }),
  ).toBeVisible()

  // A row's detail — description and attachments — is behind its own
  // disclosure now (design audit: the upload control used to repeat on
  // every row of the list).
  // exact: the status trigger's own label quotes the task title too.
  await page.getByRole('button', { name: 'Task with an attachment', exact: true }).click()

  const fileContent = `e2e attachment fixture — ${Date.now()}`
  await page.getByLabel('Choose a file to upload').setInputFiles({
    name: 'e2e-fixture.txt',
    mimeType: 'text/plain',
    buffer: Buffer.from(fileContent),
  })

  const downloadLink = page.getByRole('link', { name: 'e2e-fixture.txt' })
  await expect(downloadLink).toBeVisible()
  // Absolute href to the API's own origin — proves CI-9's plain-<a-href>
  // download design actually resolves against the right origin, not this
  // app's own (frontend and API are different origins by design).
  const expectedPrefix = new RegExp(`^${API_BASE.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}/v1/files/`)
  await expect(downloadLink).toHaveAttribute('href', expectedPrefix)

  const [download] = await Promise.all([page.waitForEvent('download'), downloadLink.click()])
  expect(download.suggestedFilename()).toBe('e2e-fixture.txt')
  const downloadedPath = await download.path()
  expect(downloadedPath).not.toBeNull()
  const downloadedContent = readFileSync(downloadedPath!, 'utf-8')
  expect(downloadedContent).toBe(fileContent)
})

test('deleting an attachment removes it from the list', async ({ page }) => {
  await registerAndLogin(page)

  await page.getByRole('button', { name: 'New task' }).click()
  await page.getByLabel(/^Title/).fill('Task for attachment delete')
  await page.getByRole('button', { name: 'Create task' }).click()
  await expect(
    page.getByRole('heading', { name: 'Task for attachment delete', level: 3 }),
  ).toBeVisible()

  await page.getByRole('button', { name: 'Task for attachment delete', exact: true }).click()

  await page.getByLabel('Choose a file to upload').setInputFiles({
    name: 'to-delete.txt',
    mimeType: 'text/plain',
    buffer: Buffer.from('will be deleted'),
  })
  const link = page.getByRole('link', { name: 'to-delete.txt' })
  await expect(link).toBeVisible()

  await page.getByRole('button', { name: 'Delete' }).last().click()
  await expect(link).toHaveCount(0)
  // The empty state carries no caption any more (design review) — the
  // upload control still being there, with no file rows left, is the
  // whole of it.
  await expect(page.getByRole('button', { name: 'Upload file' })).toBeVisible()
})
