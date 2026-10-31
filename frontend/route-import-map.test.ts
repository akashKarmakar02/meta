// E05 tests: the manually-coded route import map resolves known route IDs
// to their lazy component loaders and rejects unknown IDs explicitly.
// Runs in happy-dom (Node at test time only; production runs the Go binary
// plus prebuilt assets).
import { describe, it, expect } from 'vitest';
import { loadPage, hasPage } from './route-import-map';
import Dashboard from '../app/dashboard/page.svelte';
import Settings from '../app/dashboard/settings/page.svelte';

describe('route import map (E05)', () => {
  it('dashboard route ID resolves to the Dashboard component', async () => {
    const comp = await loadPage('dashboard/page.svelte');
    expect(comp).toBe(Dashboard);
  });

  it('settings route ID resolves to the Settings component', async () => {
    const comp = await loadPage('dashboard/settings/page.svelte');
    expect(comp).toBe(Settings);
  });

  it('unknown route IDs return an explicit missing result, no fallback', async () => {
    expect(hasPage('dashboard/page.svelte')).toBe(true);
    expect(hasPage('no/such/page.svelte')).toBe(false);
    expect(await loadPage('no/such/page.svelte')).toBeNull();
    // Never silently falls back to Dashboard for unknown IDs.
    expect(hasPage('page.templ')).toBe(false);
  });

  it('both loaders return valid mountable Svelte components', async () => {
    for (const id of ['dashboard/page.svelte', 'dashboard/settings/page.svelte']) {
      const comp = await loadPage(id);
      expect(typeof comp).toBe('function');
    }
  });

  it('serves every CSR route from the generated registry, not just E05’s manual two', async () => {
    // Since E06 the map is generated from discovery: all four CSR routes
    // (including the products placeholders) must resolve.
    const comp = await loadPage('products/new/page.svelte');
    expect(comp).toBeTruthy();
    expect(await loadPage('no/such/page.svelte')).toBeNull();
  });
});

