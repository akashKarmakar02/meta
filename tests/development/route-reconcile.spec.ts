// Route reconciliation (P2-A06 §12/§13): file additions, deletions, and
// renames reach the running application without a manual restart, and the
// nearest-layout-wins mapping stays correct.
import { test, expect } from '@playwright/test';
import { mkdirSync, rmSync } from 'node:fs';
import path from 'node:path';
import { newDevSession, write, type DevSession } from './fixtures/dev-session';

let session: DevSession;

test.beforeAll(async () => {
  session = await newDevSession();
  await session.start();
});

test.afterAll(async () => {
  await session.stop();
  session.dispose();
});

test.beforeEach(async ({ request }) => {
  await session.waitUntil(request, '/healthz', 200);
});

test('adding a route exposes it', async ({ page, request }) => {
  const routeDir = path.join(session.root, 'app/hmrroute');
  mkdirSync(routeDir);
  write(session.root, 'app/hmrroute/page.svelte', '<h1>New HMR route</h1>');
  await expect.poll(async () => { try { return (await request.get(`${session.base}/hmrroute`)).status(); } catch { return 0; } }, { timeout: 60_000 }).toBe(200);
  await page.goto(`${session.base}/hmrroute`);
  await expect(page.locator('h1')).toHaveText('New HMR route', { timeout: 30_000 });
});

test('removing a route removes it', async ({ request }) => {
  rmSync(path.join(session.root, 'app/hmrroute'), { recursive: true, force: true });
  await expect.poll(async () => { try { return (await request.get(`${session.base}/hmrroute`)).status(); } catch { return 0; } }, { timeout: 60_000 }).toBe(404);
});

test('renaming a route updates old and new URLs', async ({ page, request }) => {
  mkdirSync(path.join(session.root, 'app/hmrroute'));
  write(session.root, 'app/hmrroute/page.svelte', '<h1>Before rename</h1>');
  await expect.poll(async () => { try { return (await request.get(`${session.base}/hmrroute`)).status(); } catch { return 0; } }, { timeout: 60_000 }).toBe(200);
  // Rename the directory: the old URL must go away and the new one appear.
  const renamed = path.join(session.root, 'app/hmrrenamed');
  await (await import('node:fs')).promises.rename(path.join(session.root, 'app/hmrroute'), renamed);
  await expect.poll(async () => { try { return (await request.get(`${session.base}/hmrroute`)).status(); } catch { return 0; } }, { timeout: 60_000 }).toBe(404);
  await expect.poll(async () => { try { return (await request.get(`${session.base}/hmrrenamed`)).status(); } catch { return 0; } }, { timeout: 60_000 }).toBe(200);
  await page.goto(`${session.base}/hmrrenamed`);
  await expect(page.locator('h1')).toHaveText('Before rename', { timeout: 30_000 });
  rmSync(renamed, { recursive: true, force: true });
});

test('recursive directory deletion removes nested routes', async ({ request }) => {
  mkdirSync(path.join(session.root, 'app/hmrdir/deep'), { recursive: true });
  write(session.root, 'app/hmrdir/deep/page.svelte', '<h1>Deep route</h1>');
  await expect.poll(async () => { try { return (await request.get(`${session.base}/hmrdir/deep`)).status(); } catch { return 0; } }, { timeout: 60_000 }).toBe(200);
  rmSync(path.join(session.root, 'app/hmrdir'), { recursive: true, force: true });
  await expect.poll(async () => { try { return (await request.get(`${session.base}/hmrdir/deep`)).status(); } catch { return 0; } }, { timeout: 60_000 }).toBe(404);
});

test('nearest layout wins for a nested route', async ({ page, request }) => {
  mkdirSync(path.join(session.root, 'app/hmrlayout/inner'), { recursive: true });
  // Two candidate layouts; the route must use only the nearest one
  // (single-visual-layout contract). Layouts must carry the shell marker
  // the runtime validates (data-go-layout-shell + data-go-layout ID).
  write(session.root, 'app/hmrlayout/layout.svelte',
    '<script lang="ts">import type { Snippet } from \'svelte\'; let { children }: { children?: Snippet } = $props();</script>\n' +
    '<div data-go-layout-shell data-go-layout="app/hmrlayout/layout.svelte"><aside data-hmr-outer>outer layout</aside>' +
    '<div data-go-layout-outlet role="presentation">{@render children?.()}</div></div>');
  write(session.root, 'app/hmrlayout/inner/layout.svelte',
    '<script lang="ts">import type { Snippet } from \'svelte\'; let { children }: { children?: Snippet } = $props();</script>\n' +
    '<div data-go-layout-shell data-go-layout="app/hmrlayout/inner/layout.svelte"><aside data-hmr-inner>inner layout</aside>' +
    '<div data-go-layout-outlet role="presentation">{@render children?.()}</div></div>');
  write(session.root, 'app/hmrlayout/inner/page.svelte', '<h1>Innermost page</h1>');
  await expect.poll(async () => { try { return (await request.get(`${session.base}/hmrlayout/inner`)).status(); } catch { return 0; } }, { timeout: 60_000 }).toBe(200);
  // The layout renders client-side around the mounted page.
  await page.goto(`${session.base}/hmrlayout/inner`);
  await expect(page.locator('[data-hmr-inner]')).toBeVisible({ timeout: 30_000 });
  await expect(page.locator('[data-go-page-root] h1')).toHaveText('Innermost page');
  // Nearest layout wins: the outer layout must not render.
  await expect(page.locator('[data-hmr-outer]')).toHaveCount(0);
  rmSync(path.join(session.root, 'app/hmrlayout'), { recursive: true, force: true });
});
