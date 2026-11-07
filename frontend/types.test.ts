import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { describe, expect, it } from 'vitest';
import type { NavigationResponse } from './types';
import { NAVIGATION_HEADER, NAVIGATION_VERSION } from './types';

const fixturesDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'tests', 'fixtures');

function loadFixture(name: string): NavigationResponse {
  return JSON.parse(readFileSync(join(fixturesDir, name), 'utf-8')) as NavigationResponse;
}

describe('navigation protocol fixtures (F01)', () => {
  it('SSR fixture matches the NavigationResponse shape', () => {
    const resp = loadFixture('navigation-ssr.json');
    expect(resp.version).toBe(1);
    expect(resp.url).toBe('/about');
    expect(resp.routeId).toBe('about/page.templ');
    expect(resp.mode).toBe('ssr');
    expect(resp.title).toBe('About');
    expect(resp.metadata).toEqual({ title: 'About', description: 'Learn how Meta renders Go and Svelte pages.' });
    expect(resp.layouts).toEqual(['app/layout.templ']);
    expect(resp.html).toContain('<h1>About</h1>');
    expect(resp.clientEntry).toBeNull();
  });

  it('CSR fixture matches the NavigationResponse shape', () => {
    const resp = loadFixture('navigation-csr.json');
    expect(resp.version).toBe(1);
    expect(resp.url).toBe('/dashboard/settings');
    expect(resp.routeId).toBe('dashboard/settings/page.svelte');
    expect(resp.mode).toBe('csr');
    expect(resp.title).toBe('Settings');
    expect(resp.layouts).toEqual(['app/dashboard/layout.svelte']);
    expect(resp.html).toBe('');
    expect(resp.clientEntry).toBe('dashboard/settings/page.svelte');
  });

  it('constants match the Go side (version and request header)', () => {
    expect(NAVIGATION_VERSION).toBe(1);
    expect(NAVIGATION_HEADER).toBe('X-Go-Web-Navigation');
  });
});
