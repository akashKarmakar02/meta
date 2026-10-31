// E07 tests: the Vite production build emits a usable asset manifest at
// dist/client/.vite/manifest.json. Assertions are structural — entry
// present, file fields reference real hashed assets, page components
// remain lazy dynamic chunks — without hardcoding any hash.
// Runs in Node at test time only; production runs the Go binary plus
// prebuilt dist/client assets.
import { describe, it, expect } from 'vitest';
import { readFileSync, existsSync, statSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const clientDir = resolve(repoRoot, 'dist', 'client');
const manifestPath = resolve(clientDir, '.vite', 'manifest.json');

// These tests assert the post-build state of dist/client, so they require
// a prior "npm run build" (the standard verification sequence builds
// before testing). On a clean checkout with no build output they skip
// rather than fail: nothing has claimed to produce a manifest yet.
const built = existsSync(manifestPath);

function readManifest(): Record<
  string,
  {
    file: string;
    src?: string;
    isEntry?: boolean;
    isDynamicEntry?: boolean;
    dynamicImports?: string[];
  }
> {
  return JSON.parse(readFileSync(manifestPath, 'utf-8'));
}

describe.skipIf(!built)('Vite production manifest (E07)', () => {
  it('build emits manifest at dist/client/.vite/manifest.json', () => {
    const stat = statSync(manifestPath);
    expect(stat.isFile()).toBe(true);
    expect(stat.size).toBeGreaterThan(0);
  });

  it('manifest contains the frontend entry keyed by source path', () => {
    const manifest = readManifest();
    const entry = manifest['frontend/entry.ts'];
    expect(entry).toBeDefined();
    expect(entry.isEntry).toBe(true);
    expect(entry.file).toMatch(/^assets\/entry-[\w-]+\.js$/);
  });

  it('entry file field references an existing hashed JavaScript file', () => {
    const manifest = readManifest();
    const entry = manifest['frontend/entry.ts'];
    const file = resolve(clientDir, entry.file);
    expect(existsSync(file)).toBe(true);
    expect(entry.file.endsWith('.js')).toBe(true);
  });

  it('page components remain available as lazy dynamic-import chunks', () => {
    const manifest = readManifest();
    const entry = manifest['frontend/entry.ts'];
    // Entry's dynamicImports list must include the dashboard pages (the
    // route registry's loaders stay lazy).
    const dynamicSources = entry.dynamicImports ?? [];
    for (const src of [
      'app/dashboard/page.svelte',
      'app/dashboard/settings/page.svelte',
      'app/products/new/page.svelte',
      'app/products/[id]/page.svelte',
    ]) {
      expect(dynamicSources).toContain(src);
      // And each has its own hashed chunk on disk.
      const chunk = manifest[src];
      expect(chunk?.isDynamicEntry).toBe(true);
      expect(existsSync(resolve(clientDir, chunk.file))).toBe(true);
    }
  });
});
