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
    environment: 'happy-dom',
  },
});
