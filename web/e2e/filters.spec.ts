import { createTask, expect, moveToStatus, registerAndLogin, test } from './fixtures'

// Regression: the whole header — filters included — used to unmount on
// every fetch, which closed an open filter menu the instant a toggle
// inside it triggered the very fetch that unmounted it. Ticking a
// second box without reopening the menu is exactly what that bug broke.
test('selecting Done narrows the list, selecting Cancelled widens it, and the menu stays open between toggles', async ({
  page,
}) => {
  await registerAndLogin(page)

  await createTask(page, 'Filter task pending')
  await createTask(page, 'Filter task done')
  await createTask(page, 'Filter task cancelled')

  await moveToStatus(page, 'Filter task done', 'Done')
  await moveToStatus(page, 'Filter task cancelled', 'Cancelled')

  const pendingHeading = page.getByRole('heading', { name: 'Filter task pending', level: 3 })
  const doneHeading = page.getByRole('heading', { name: 'Filter task done', level: 3 })
  const cancelledHeading = page.getByRole('heading', { name: 'Filter task cancelled', level: 3 })

  // Default filter: pending + in_progress + done, cancelled hidden.
  await expect(pendingHeading).toBeVisible()
  await expect(doneHeading).toBeVisible()
  await expect(cancelledHeading).toHaveCount(0)

  await page.getByRole('button', { name: 'Filter by status' }).click()
  const doneItem = page.getByRole('menuitemcheckbox', { name: 'Done' })
  await expect(doneItem).toHaveAttribute('aria-checked', 'true')

  // Narrow: untick Done.
  await doneItem.click()
  await expect(doneHeading).toHaveCount(0)
  await expect(pendingHeading).toBeVisible()

  // The menu must still be open — no click on the trigger between this
  // and the previous toggle — for the next toggle to even be reachable.
  const cancelledItem = page.getByRole('menuitemcheckbox', { name: 'Cancelled' })
  await expect(cancelledItem).toBeVisible()

  // Widen: tick Cancelled.
  await cancelledItem.click()
  await expect(cancelledHeading).toBeVisible()
  await expect(doneHeading).toHaveCount(0)
})

test('unticking the last remaining status is refused', async ({ page }) => {
  await registerAndLogin(page)

  await createTask(page, 'Only status task')

  await page.getByRole('button', { name: 'Filter by status' }).click()
  await page.getByRole('menuitemcheckbox', { name: 'Pending' }).click()
  await page.getByRole('menuitemcheckbox', { name: 'In progress' }).click()

  const doneItem = page.getByRole('menuitemcheckbox', { name: 'Done' })
  await expect(doneItem).toHaveAttribute('aria-checked', 'true')

  // Done is now the one remaining selected status — unticking it would
  // ask the server for nothing, which it cannot express (an absent
  // filter means "no filter"), so the UI must refuse this click rather
  // than let the selection reach zero.
  await doneItem.click()

  await expect(doneItem).toHaveAttribute('aria-checked', 'true')
})

// Regression: TaskItem's edit path used to patch the row in place from
// the mutation response without re-checking whether the task still
// matched the active filter — cancelling a task while cancelled is
// filtered out left it sitting on screen, badge and all, until
// something unrelated caused a fresh fetch.
test('cancelling a task that cancelled is filtered out of removes it from the screen', async ({
  page,
}) => {
  await registerAndLogin(page)

  await createTask(page, 'Task to cancel')
  const heading = page.getByRole('heading', { name: 'Task to cancel', level: 3 })
  await expect(heading).toBeVisible()

  await moveToStatus(page, 'Task to cancel', 'Cancelled')

  await expect(heading).toHaveCount(0)
})
