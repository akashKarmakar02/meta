import { defineConfig } from '@playwright/test'

/**
 * P2-F03: focused native-image run. Separate from the default
 * playwright.config.ts on purpose: that suite runs the untagged build
 * (no native headers needed); this one boots a binary compiled with
 * -tags imageproc_native and runs only the image specs. Run via
 * `make e2e-native` after `make build-native`.
 */
export default defineConfig({
  testDir: './tests/browser',
  testMatch: /f0[345]-image.*\.spec\.ts/,
  timeout: 30_000,
  retries: 0,
  use: {
    baseURL: 'http://127.0.0.1:3139',
    trace: 'off',
  },
  webServer: {
    // F05: cache enabled with a temporary directory so this suite
    // exercises real encode→disk-hit cycles.
    command:
      'PORT=3139 GO_META_IMAGE_CACHE_DIR="${GO_META_NATIVE_TEST_CACHE:-/tmp/go-meta-f05-native-cache}" ./bin/example-native',
    url: 'http://127.0.0.1:3139/healthz',
    // F04: never attach to an already-running server — a stale tagged
    // binary answering probes caused a false pass during F03R.
    reuseExistingServer: false,
    timeout: 20_000,
  },
})
