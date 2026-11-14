import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { fileURLToPath } from 'node:url';

export default defineConfig({
  plugins: [{ name: 'clerk-fixture-registry', enforce: 'pre', resolveId(source, importer) {
    if (source === './route-registry.generated' && importer?.endsWith('/frontend/route-import-map.ts')) {
      return fileURLToPath(new URL('./registry.ts', import.meta.url));
    }
  } }, svelte()],
  publicDir: false, envDir: false,
  build: { manifest: true, outDir: 'dist/clerk-fixture', rollupOptions: { input: 'frontend/entry.ts' } },
});
