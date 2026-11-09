import { defineConfig } from '@playwright/test';

// CORS browser-enforcement suite (docs/guides/cors.md). Separate from the
// default production suite: these checks need a second server instance with
// CORS opted in through GO_META_CORS_ORIGINS, plus page origins the browser
// genuinely treats as cross-origin (localhost vs 127.0.0.1).
//
// - Server A (3131): plain production example binary, serves the host pages.
// - Server B (3141): same binary with CORS enabled, allowing A's origin,
//   credentialed. This is the API under test.
export default defineConfig({
  testDir: 'tests/cors',
  retries: 0,
  workers: 1,
  timeout: 30_000,
  reporter: [['list']],
  use: {
    baseURL: 'http://localhost:3131',
  },
  webServer: [
    {
      command: 'PORT=3131 ./bin/example',
      port: 3131,
      reuseExistingServer: false,
      stdout: 'ignore',
      stderr: 'pipe',
      timeout: 15_000,
    },
    {
      command:
        'PORT=3141 GO_META_TRUSTED_ORIGIN=http://127.0.0.1:3141 GO_META_CORS_ORIGINS=http://localhost:3131 GO_META_CORS_CREDENTIALS=1 ./bin/example',
      port: 3141,
      reuseExistingServer: false,
      stdout: 'ignore',
      stderr: 'pipe',
      timeout: 15_000,
    },
  ],
});
