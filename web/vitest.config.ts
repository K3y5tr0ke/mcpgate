import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    // Vitest owns the unit/component tests under src/. Playwright owns the
    // browser E2E specs under tests/e2e/ — exclude them so Vitest does not try
    // to collect @playwright/test specs.
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
    exclude: ['tests/e2e/**', 'node_modules/**'],
  },
})
