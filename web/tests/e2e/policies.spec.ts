import { test, expect } from '@playwright/test'

// Round-trip toggle of the seeded (admin, *, *) policy cell — leaves it as found.
test('policy matrix toggles the admin wildcard cell', async ({ page }) => {
  await page.goto('/console/roles')
  await expect(page.getByRole('heading', { name: 'Roles' })).toBeVisible()

  // The admin → * → * cell carries a title describing its current state.
  const cell = page.getByTitle(/^admin → \* → \*:/)
  await expect(cell).toHaveAttribute('title', /allowed/)

  // Remove it → becomes denied.
  await cell.click()
  await expect(cell).toHaveAttribute('title', /denied/)

  // Re-add it → back to allowed (restores seeded state).
  await cell.click()
  await expect(cell).toHaveAttribute('title', /allowed/)
})
