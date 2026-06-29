import { test, expect } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'

// Accessibility audit (Sprint 10, S9-D5). Runs axe-core against the key
// authenticated console pages and fails on any serious/critical WCAG 2 A/AA
// violation. Moderate/minor findings are reported but do not fail the gate, so
// the audit can be tightened incrementally. Reuses the admin storageState from
// auth.setup (chromium project), so these run as a logged-in admin.

const pages = [
  { name: 'dashboard', path: '/console/dashboard', heading: 'Dashboard' },
  { name: 'servers', path: '/console/servers', heading: 'Servers' },
  { name: 'roles', path: '/console/roles', heading: 'Roles' },
  { name: 'logs', path: '/console/logs', heading: 'Audit logs' },
  { name: 'settings', path: '/console/settings', heading: 'Settings' },
  { name: 'users', path: '/console/users', heading: 'Users' },
]

for (const p of pages) {
  test(`a11y: ${p.name} has no serious/critical violations`, async ({ page }) => {
    await page.goto(p.path)
    // Ensure the SPA view has rendered before scanning.
    await expect(page.getByRole('heading', { name: p.heading })).toBeVisible()

    const results = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa'])
      .analyze()

    const blocking = results.violations.filter(
      (v) => v.impact === 'serious' || v.impact === 'critical',
    )
    const summary = blocking
      .map((v) => `  ${v.id} (${v.impact}): ${v.help} [${v.nodes.length} node(s)]`)
      .join('\n')

    expect(blocking.length, `serious/critical a11y violations on ${p.name}:\n${summary}`).toBe(0)
  })
}
