import { createTask, expect, registerAndLogin, test } from './fixtures'

// Mirrors src/features/tasks/useTasks.tsx's exported PAGE_SIZE as a
// literal, the same reason env.ts's API_BASE mirrors src/api/client.ts's
// fallback instead of importing it — this suite exercises the built app
// as a black box, never its source modules directly.
const PAGE_SIZE = 10

test('page 1 holds ten rows and the rest sits on page 2; Previous is disabled on the first page, Next on the last', async ({
  page,
}) => {
  await registerAndLogin(page)

  for (let i = 1; i <= PAGE_SIZE + 2; i++) {
    await createTask(page, `Pagination task ${i}`)
  }

  const rowHeadings = page.getByRole('list').getByRole('heading', { level: 3 })

  await expect(page.getByText('Page 1')).toBeVisible()
  await expect(rowHeadings).toHaveCount(PAGE_SIZE)
  await expect(page.getByRole('button', { name: 'Previous' })).toBeDisabled()
  await expect(page.getByRole('button', { name: 'Next' })).toBeEnabled()

  await page.getByRole('button', { name: 'Next' }).click()

  await expect(page.getByText('Page 2')).toBeVisible()
  await expect(rowHeadings).toHaveCount(2)
  await expect(page.getByRole('button', { name: 'Next' })).toBeDisabled()
  await expect(page.getByRole('button', { name: 'Previous' })).toBeEnabled()

  await page.getByRole('button', { name: 'Previous' }).click()

  await expect(page.getByText('Page 1')).toBeVisible()
  await expect(rowHeadings).toHaveCount(PAGE_SIZE)
})

test('the pager is absent when every task fits on one page', async ({ page }) => {
  await registerAndLogin(page)

  await createTask(page, 'Only task')

  await expect(page.getByRole('heading', { name: 'Only task', level: 3 })).toBeVisible()
  await expect(page.getByRole('navigation', { name: 'Task pages' })).toHaveCount(0)
})

// Regression: the status/priority filter menus used to fire their fetch
// at whatever offset the previous page had scrolled to, landing a
// caller who changed a filter from page 2 on a "page 2" that made no
// sense for the newly filtered set.
test('changing a filter returns to page 1', async ({ page }) => {
  await registerAndLogin(page)

  for (let i = 1; i <= PAGE_SIZE + 2; i++) {
    await createTask(page, `Reset task ${i}`)
  }

  await page.getByRole('button', { name: 'Next' }).click()
  await expect(page.getByText('Page 2')).toBeVisible()

  // Every created task defaults to medium priority, so unticking "Low"
  // changes the active filter without changing which tasks match it —
  // isolating the page-reset behavior from any change in result size.
  await page.getByRole('button', { name: 'Filter by priority' }).click()
  await page.getByRole('menuitemcheckbox', { name: 'Low' }).click()

  await expect(page.getByText('Page 1')).toBeVisible()
})
