import { defineConfig } from 'vitest/config'

export default defineConfig({
  esbuild: {
    jsx: 'automatic',
    jsxImportSource: 'preact',
  },
  build: {
    // Build straight into the directory embedded into the binary via go:embed.
    // go:embed cannot reach through "..", so there is no intermediate copy step.
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    target: 'es2022',
    sourcemap: true,
  },
  server: {
    port: 5173,
    // In development the frontend runs separately and the API is proxied to the Go process.
    proxy: {
      '/api': 'http://127.0.0.1:8071',
    },
  },
  test: {
    environment: 'node',
    setupFiles: ['./src/test/setup.ts'],
    // End-to-end scenarios are run by Playwright: it has its own runner and a real browser.
    include: ['src/**/*.test.ts'],
  },
})
