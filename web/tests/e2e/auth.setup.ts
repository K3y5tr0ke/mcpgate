import { test as setup, expect } from '@playwright/test'

const authFile = 'tests/e2e/.auth/admin.json'

// Logs in once as the local admin and persists the session (cookie + the
// mcpgate_token in localStorage) so every other spec reuses it via storageState.
setup('authenticate', async ({ page }) => {
  await page.goto('/console/login')
  await page.fill('#email', 'admin@localhost')
  await page.fill('#password', process.env.ADMIN_PASSWORD ?? 'changeme')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await page.waitForURL('**/console/dashboard')
  await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible()
  await page.context().storageState({ path: authFile })
})
