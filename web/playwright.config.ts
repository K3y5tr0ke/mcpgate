import { defineConfig, devices } from '@playwright/test'

// Playwright drives a real Chromium browser against a running mcpgate stack
// (server + Postgres + Redis). It is NOT part of ci-fast; it runs in the
// Docker-backed `ci` target / CI job. See Makefile `test-e2e-playwright`.
export default defineConfig({
  testDir: './tests/e2e',
  fullyParallel: false,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? 'http://localhost:8080',
    storageState: 'tests/e2e/.auth/admin.json',
    trace: 'on-first-retry',
  },
  projects: [
    // The setup project CREATES the storageState file, so it must not try to
    // LOAD it (it is gitignored and absent on a clean CI checkout).
    { name: 'setup', testMatch: '**/auth.setup.ts', use: { storageState: undefined } },
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
      dependencies: ['setup'],
    },
  ],
})
