// Persistent Svelte layouts through the development proxy (P2-A06 §13/§14):
// the dashboard counter survives sibling navigation, child-page HMR does
// not remount the layout, and exiting/re-entering the subtree behaves like
// a fresh layout.
import { test, expect } from '@playwright/test';
import { newDevSession, read, write, type DevSession } from './fixtures/dev-session';

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

test('dashboard counter survives sibling navigation', async ({ page }) => {
  await page.goto(`${session.base}/about`);
  await page.click('a[href="/dashboard"]');
  await expect(page.locator('[data-go-page="dashboard"]')).toBeVisible();
  await page.locator('[data-go-layout-counter-button]').click();
  await expect(page.locator('[data-go-layout-counter]')).toContainText('1');
  await page.click('[data-go-dashboard-side-nav] a[href="/dashboard/settings"]');
  await expect(page.locator('[data-go-page="dashboard/settings"]')).toBeVisible();
  await page.click('[data-go-dashboard-side-nav] a[href="/dashboard"]');
  await expect(page.locator('[data-go-page="dashboard"]')).toBeVisible();
  // Soft navigation preserved the layout instance: the counter survived.
  await expect(page.locator('[data-go-layout-counter]')).toContainText('1');
});

test('child-page HMR does not remount the persistent layout', async ({ page }) => {
  await page.goto(`${session.base}/dashboard`);
  await expect(page.locator('[data-go-page="dashboard"]')).toBeVisible();
  await page.locator('[data-go-layout-counter-button]').click();
  await expect(page.locator('[data-go-layout-counter]')).toContainText('1');
  await page.evaluate(() => { (window as any).__hmrDocument = 'same'; (window as any).__hmrLayout = document.querySelector('[data-go-layout-shell]'); });
  const pageSource = read(session.root, 'app/dashboard/page.svelte');
  write(session.root, 'app/dashboard/page.svelte', pageSource.replace('<h1>Dashboard</h1>', '<h1>Dashboard HMR updated</h1>'));
  await expect(page.locator('h1')).toHaveText('Dashboard HMR updated', { timeout: 30_000 });
  // The page updated in place: same document, same layout element, and the
  // layout's own state (the counter) was not remounted away.
  expect(await page.evaluate(() => (window as any).__hmrDocument)).toBe('same');
  expect(await page.evaluate(() => (window as any).__hmrLayout === document.querySelector('[data-go-layout-shell]'))).toBe(true);
  await expect(page.locator('[data-go-layout-counter]')).toContainText('1');
});

test('layout edit updates without remounting the document', async ({ page }) => {
  await page.goto(`${session.base}/dashboard`);
  await expect(page.locator('[data-go-page="dashboard"]')).toBeVisible();
  await page.evaluate(() => { (window as any).__hmrDocument = 'same'; });
  const original = read(session.root, 'app/dashboard/layout.svelte');
  write(session.root, 'app/dashboard/layout.svelte', original.replace('<h2>Dashboard</h2>', '<h2>Dashboard layout HMR</h2>'));
  await expect(page.locator('[data-go-dashboard-sidebar] h2')).toHaveText('Dashboard layout HMR', { timeout: 30_000 });
  expect(await page.evaluate(() => (window as any).__hmrDocument)).toBe('same');
});

test('exiting and re-entering the subtree restores fresh-layout behavior', async ({ page }) => {
  await page.goto(`${session.base}/dashboard`);
  await expect(page.locator('[data-go-page="dashboard"]')).toBeVisible();
  await page.locator('[data-go-layout-counter-button]').click();
  await expect(page.locator('[data-go-layout-counter]')).toContainText('1');
  // Exit to a sibling outside the subtree, then re-enter. /about is a
  // templ SSR page marked data-go-page-about.
  await page.goto(`${session.base}/about`);
  await expect(page.locator('[data-go-page-about]')).toBeVisible();
  await page.click('a[href="/dashboard"]');
  await expect(page.locator('[data-go-page="dashboard"]')).toBeVisible();
  // G06 semantics: re-entering mounts a fresh layout — the counter reset.
  await expect(page.locator('[data-go-layout-counter]')).toContainText('0');
});
