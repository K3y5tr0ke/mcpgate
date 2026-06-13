import { test, expect } from '@playwright/test'

test('audit logs export downloads a CSV', async ({ page }) => {
  await page.goto('/console/logs')
  await expect(page.getByRole('heading', { name: 'Audit logs' })).toBeVisible()

  const downloadPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export CSV' }).click()
  const download = await downloadPromise

  expect(download.suggestedFilename()).toMatch(/\.csv$/)
})
