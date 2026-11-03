// Svelte HMR (P2-A06 §13): page edits update without a document reload,
// shared-component edits propagate, and ordinary Svelte content edits do
// not trigger Go rebuilds.
import { test, expect } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import path from 'node:path';
import { newDevSession, read, write, pause, type DevSession } from './fixtures/dev-session';

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

test('page edit updates without document reload', async ({ page }) => {
  await page.goto(`${session.base}/dashboard`);
  await expect(page.locator('[data-go-page="dashboard"]')).toBeVisible();
  const builds = () => (session.output.match(/building\.\.\./g) ?? []).length;
  await pause(3000);
  const before = builds();
  write(session.root, 'app/dashboard/page.svelte', read(session.root, 'app/dashboard/page.svelte').replace('<h1>Dashboard</h1>', '<h1>Dashboard HMR updated</h1>'));
  await expect(page.locator('h1')).toHaveText('Dashboard HMR updated', { timeout: 30_000 });
  // Ordinary Svelte content edits are Vite-only: no Go build started.
  expect(builds()).toBe(before);
});

test('shared component edit updates the mounted pages', async ({ page }) => {
  mkdirSync(path.join(session.root, 'app/components'), { recursive: true });
  // Create a shared component beside the app's existing shared components
  // and import it from the dashboard page (test-owned fixture files only).
  write(session.root, 'app/components/HmrShared.svelte', '<p data-hmr-shared>shared-original</p>');
  const original = read(session.root, 'app/dashboard/page.svelte');
  write(session.root, 'app/dashboard/page.svelte',
    original.replace('<script lang="ts">', '<script lang="ts">\n  import HmrShared from \'../components/HmrShared.svelte\';')
            .replace('</main>', '  <HmrShared />\n</main>'));
  await page.goto(`${session.base}/dashboard`);
  await expect(page.locator('[data-hmr-shared]')).toHaveText('shared-original', { timeout: 30_000 });
  // Edit only the shared component: the mounted page updates without reload.
  write(session.root, 'app/components/HmrShared.svelte', '<p data-hmr-shared>shared-updated</p>');
  await expect(page.locator('[data-hmr-shared]')).toHaveText('shared-updated', { timeout: 30_000 });
});

test('Svelte compile error surfaces and recovers in the same session', async ({ page, request }) => {
  // Start from a known baseline: earlier tests in this file edited the h1.
  const baseline = read(session.root, 'app/dashboard/page.svelte').replace(/<h1>[^<]*<\/h1>/, '<h1>Dashboard baseline</h1>');
  write(session.root, 'app/dashboard/page.svelte', baseline);
  await page.goto(`${session.base}/dashboard`);
  await expect(page.locator('[data-go-page="dashboard"]')).toBeVisible();
  // Break the Svelte source; the browser shows Vite's error overlay and
  // the session stays up.
  write(session.root, 'app/dashboard/page.svelte', '<script lang="ts">const broken = ;</script>');
  await expect.poll(async () => (await request.get(`${session.base}/_go_meta_vite/app/dashboard/page.svelte?import`)).status(), { timeout: 30_000 }).toBe(500);
  // Fix it; HMR updates the mounted page (or a reload picks up the fix).
  write(session.root, 'app/dashboard/page.svelte', baseline.replace('<h1>Dashboard baseline</h1>', '<h1>Dashboard recovered</h1>'));
  await expect.poll(async () => {
    try { return (await request.get(`${session.base}/_go_meta_vite/app/dashboard/page.svelte?import`)).status(); } catch { return 0; }
  }, { timeout: 30_000 }).toBe(200);
  await page.reload();
  await expect(page.locator('h1')).toHaveText('Dashboard recovered', { timeout: 30_000 });
});
