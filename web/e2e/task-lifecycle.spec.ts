import { expect, registerAndLogin, test } from './fixtures'

test('create, edit, transition status, then delete a task with confirmation', async ({ page }) => {
  await registerAndLogin(page)

  // level: 3 — TaskItem's own title is an <h3>; the delete-confirm
  // dialog's title is an <h2> that also contains the task's name, and
  // getByRole name-matching is substring, so an unscoped query would
  // ambiguously match both once the confirm dialog is open.
  const taskHeading = page.getByRole('heading', { name: 'E2E lifecycle task', level: 3 })
  const editedHeading = page.getByRole('heading', {
    name: 'E2E lifecycle task (edited)',
    level: 3,
  })

  // --- Create ---
  await page.getByRole('button', { name: 'New task' }).click()
  await page.getByLabel(/^Title/).fill('E2E lifecycle task')
  await page.getByRole('button', { name: 'Create task' }).click()
  await expect(taskHeading).toBeVisible()

  // --- Edit ---
  await page.getByRole('button', { name: 'Edit' }).click()
  const titleField = page.getByLabel(/^Title/)
  await titleField.fill('E2E lifecycle task (edited)')
  await page.getByRole('button', { name: 'Save changes' }).click()
  await expect(editedHeading).toBeVisible()

  // --- Status transition (Fase 14 CI-12: icon-trigger pull-down menu) ---
  await page.getByRole('button', { name: /Change status of/ }).click()
  await page.getByRole('menuitem', { name: 'Move to In progress' }).click()
  await expect(page.getByText('in_progress')).toBeVisible()

  // --- Delete, requires confirmation ---
  await page.getByRole('button', { name: 'Delete' }).click()
  const confirmDialog = page.getByRole('dialog', { name: /^Delete/ })
  await expect(confirmDialog).toBeVisible()
  // The task must still be there — no request should have fired yet.
  await expect(editedHeading).toBeVisible()

  await confirmDialog.getByRole('button', { name: 'Delete' }).click()
  await expect(editedHeading).toHaveCount(0)
})
