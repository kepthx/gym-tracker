import { defineConfig, devices } from '@playwright/test'

const PORT = 8099

/**
 * The end-to-end tests run against the real binary with a real database: what gets checked
 * is exactly what ships to the server, not a stand-in API implementation.
 */
export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  timeout: 60_000,
  reporter: process.env.CI ? 'list' : [['list']],

  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    trace: 'retain-on-failure',
    // The app is opened from a phone — the viewport is sized to match.
    ...devices['iPhone 14'],
    isMobile: false, // Chromium engine, so touch emulation without WebKit is unnecessary
    hasTouch: false,
  },

  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],

  webServer: {
    command: `node e2e/server.mjs ${PORT}`,
    url: `http://127.0.0.1:${PORT}/healthz`,
    reuseExistingServer: false,
    stdout: 'pipe',
    stderr: 'pipe',
    timeout: 60_000,
  },
})
