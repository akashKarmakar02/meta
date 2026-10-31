import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// Minimal Phase 1 (A05) configuration: build the client entry bundle.
// Route registration, manifests, and dist/client output layout arrive with
// later tasks (E06/E07) per the blueprint.
export default defineConfig({
  plugins: [svelte()],
  build: {
    rollupOptions: {
      input: 'frontend/entry.ts',
    },
  },
});
