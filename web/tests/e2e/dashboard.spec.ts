import { test, expect } from '@playwright/test'

test('dashboard shows KPIs', async ({ page }) => {
  await page.goto('/console/dashboard')
  await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible()
  // At least one stat card label should be present (the dashboard renders KPI cards).
  await expect(page.locator('body')).toContainText(/req|session|latency|requests/i)
})
