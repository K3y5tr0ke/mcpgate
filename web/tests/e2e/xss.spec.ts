import { test, expect } from '@playwright/test'

// XSS prevention (Sprint 10, task 10.5). Stores an XSS payload as a server name
// via the admin API, then renders the servers page and asserts the payload is
// NOT executed (React escaping + the CSP from 10.2) and appears as literal text.
// If the name were ever rendered via dangerouslySetInnerHTML, the injected
// <img onerror> would fire and set the sentinel — failing this test.
test('stored XSS payload in a server name is rendered inert', async ({ page }) => {
  // Server names are unique, so namespace the payload per run; otherwise a
  // re-run against the same DB would 409 on create. The XSS markup is preserved.
  const marker = `xss-${Date.now()}`
  const payload = `<img src=x onerror="window.__xssExecuted=true">${marker}`

  // Load a console page first so the admin token is in localStorage.
  await page.goto('/console/dashboard')
  const token = await page.evaluate(() => localStorage.getItem('mcpgate_token'))
  expect(token, 'admin token should be in localStorage').toBeTruthy()

  // Store the payload as a server name through the admin API.
  const res = await page.request.post('/api/v1/admin/servers', {
    headers: { Authorization: `Bearer ${token}` },
    data: {
      name: payload,
      transport: 'streamable_http',
      config: { endpoint: 'http://127.0.0.1:1' },
      is_enabled: true,
    },
  })
  expect(res.status(), 'create server with payload name').toBe(201)
  const createdId = (await res.json()).id as string

  try {
    // Render the page that lists server names.
    await page.goto('/console/servers')
    await expect(page.getByRole('heading', { name: 'Servers' })).toBeVisible()

    // The payload must appear as literal, escaped text...
    await expect(page.locator('body')).toContainText('onerror=')

    // ...and must NOT have executed (no element was created from the markup).
    const executed = await page.evaluate(
      () => (window as unknown as { __xssExecuted?: boolean }).__xssExecuted === true,
    )
    expect(executed, 'XSS payload must not execute').toBe(false)
  } finally {
    // Clean up so the run is idempotent and leaves no junk upstream behind.
    if (createdId) {
      await page.request.delete(`/api/v1/admin/servers/${createdId}`, {
        headers: { Authorization: `Bearer ${token}` },
      })
    }
  }
})
