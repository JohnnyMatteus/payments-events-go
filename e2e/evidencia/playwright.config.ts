import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  timeout: 10 * 60_000,
  workers: 1,
  fullyParallel: false,
  retries: 0,
  outputDir: './test-results',
  reporter: 'list',
  use: {
    viewport: { width: 1280, height: 720 },
    video: { mode: 'on', size: { width: 1280, height: 720 } },
    launchOptions: { slowMo: 600 },
    actionTimeout: 20_000,
    navigationTimeout: 30_000,
  },
});
