import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './test/browser',
  timeout: 30_000,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['line'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: process.env.E2E_RELAY_URL || 'http://127.0.0.1:3334',
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
});
