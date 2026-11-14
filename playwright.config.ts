// Playwright E2E config: real-browser verification against the production
// Go binary + Vite assets (blueprint §7: browser/navigation.spec.ts).
//
// The webServer boots the REAL built server (make build's output), not a
// dev harness — E2E asserts production behavior: DOM identity of retained
// layouts, persistent state across child-only swaps, no-reload soft
// navigation. Unit-level behavior stays in vitest; this file exists for
// the checks only a real Chromium can make.
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: 'tests/browser',
  // Real-browser runs are heavier than unit tests; keep retries at zero so
  // a flake surfaces as a failure, never silently passes on retry.
  retries: 0,
  workers: 1, // one server, one browser context at a time
  timeout: 30_000,
  reporter: [['list']],
  use: {
    baseURL: 'http://localhost:3131',
    // Trusted input is the point: these tests click like users and verify
    // the router's trust boundary (F05) doesn't interfere.
  },
  webServer: [{
    // PORT picks the scratch port; the binary itself comes from `make
    // build` and is NOT rebuilt here — a stale binary is a build bug, not
    // an E2E failure to hide behind retries.
    command: 'PORT=3131 ./bin/example',
    port: 3131,
    reuseExistingServer: false,
    stdout: 'ignore',
    stderr: 'pipe',
    timeout: 15_000,
  }, {
    command: 'PORT=3133 ./bin/pagecache-fixture',
    port: 3133,
    reuseExistingServer: false,
    stdout: 'ignore',
    stderr: 'pipe',
    timeout: 15_000,
  }, {
    command: 'PORT=3135 ./bin/clerk-fixture',
    port: 3135,
    reuseExistingServer: false,
    stdout: 'ignore',
    stderr: 'pipe',
    timeout: 15_000,
  }],
});
