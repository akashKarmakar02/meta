import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// Vitest configuration for frontend lifecycle tests (E04). Uses the
// same Svelte plugin as the production build so components under test
// are compiled exactly as they ship. Node is a dev-time concern only:
// production runs the Go binary plus prebuilt client assets.
export default defineConfig({
  plugins: [svelte()],
  // Node resolves the 'svelte' package to its server entry by default;
  // lifecycle tests exercise the client runtime, so request browser
  // conditions (index-client.js with real mount/unmount).
  resolve: {
    conditions: ['browser'],
  },
  test: {
    // Playwright specs run under their own runner (`make e2e`); vitest
    // must not double-execute them (test.describe is runner-specific).
    exclude: ['**/node_modules/**', '**/dist/**', 'tests/browser/**'],
    environment: 'happy-dom',
    environmentOptions: {
      happyDOM: {
        settings: {
          // The optional live-server integration check (F06) fetches a
          // real local Go server from a different simulated origin; real
          // browsers are always same-origin with their own pages, so
          // this only relaxes the simulation, never production behavior.
          fetch: {
            disableSameOriginPolicy: true,
          },
        },
      },
    },
  },
});
