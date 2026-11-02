import { defineConfig } from '@playwright/test';

// Separate from production E2E: this suite starts its own isolated meta dev.
export default defineConfig({
  testDir: 'tests/development',
  workers: 1,
  timeout: 180_000,
  reporter: [['list']],
});
