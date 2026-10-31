import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// Production client build (E07): explicit entry, Vite asset manifest on,
// output under dist/client. The backend reads dist/client/.vite/manifest.json
// at startup (E08) to emit hashed asset URLs — hashed filenames stay
// Vite-controlled; nothing hardcodes them. Static serving of dist/client
// arrives with E09.
export default defineConfig({
  plugins: [svelte()],
  build: {
    manifest: true,
    outDir: 'dist/client',
    rollupOptions: {
      input: 'frontend/entry.ts',
    },
  },
});
