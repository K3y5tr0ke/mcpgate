import { test, expect } from '@playwright/test'

// Unique name so reruns don't collide with leftover rows.
const serverName = `e2e-srv-${Date.now()}`

test('create, edit-open, and delete a server', async ({ page }) => {
  await page.goto('/console/servers')
  await expect(page.getByRole('heading', { name: 'Servers' })).toBeVisible()

  // Create
  await page.getByRole('button', { name: 'Add server' }).click()
  await page.getByLabel('Name').fill(serverName)
  await page.getByPlaceholder('https://my-mcp-server/mcp').fill('http://localhost:9999/mcp')
  await page.getByRole('button', { name: 'Create' }).click()
  await expect(page.getByText(serverName)).toBeVisible()

  // Edit opens a pre-populated modal
  const row = page.locator('tr', { hasText: serverName })
  await row.getByRole('button', { name: 'Edit' }).click()
  await expect(page.getByRole('heading', { name: 'Edit server' })).toBeVisible()
  await page.getByRole('button', { name: 'Cancel' }).click()

  // Delete (auto-accept the confirm dialog)
  page.on('dialog', (d) => d.accept())
  await row.getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByText(serverName)).toHaveCount(0)
})
