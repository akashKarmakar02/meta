import { readFileSync } from 'node:fs';
import { test, expect, type Route } from '@playwright/test';

const manifest = JSON.parse(readFileSync('dist/client/.vite/manifest.json', 'utf8')) as Record<string, { file: string; css?: string[] }>;
const progress = '[data-meta-navigation-progress]';

test('delayed response keeps outgoing page and focus, shows accessible reduced-motion progress, cleans up', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('/dashboard');
  await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
  let request: Route | undefined;
  await page.route('**/dashboard/settings', async (route) => { request = route; });
  await page.click('a[href="/dashboard/settings"]');
  await expect(page.locator(progress)).toHaveCount(1);
  await expect(page.getByRole('status')).toHaveText('Loading page');
  await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
  await expect(page.locator('a[href="/dashboard/settings"]')).toBeFocused();
  expect(await page.locator(`${progress} .bar`).evaluate((bar) => getComputedStyle(bar).animationName)).toBe('none');
  expect(await page.locator(progress).evaluate((host) => host.parentElement === document.body)).toBe(true);
  await request!.continue();
  await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');
  await expect(page.locator(progress)).toHaveCount(0);
  await expect(page.locator('[data-go-page-root]')).toBeFocused();
  await expect(page).toHaveURL(/\/dashboard\/settings$/);
});

test('destination JS and CSS downloads preserve the page and retained layout until ready', async ({ page }) => {
  await page.goto('/dashboard');
  await page.click('[data-go-layout-counter-button]');
  const destination = manifest['app/dashboard/settings/page.svelte'];
  expect(destination.css?.length).toBeGreaterThan(0);
  let script: Route | undefined;
  let stylesheet: Route | undefined;
  await page.route(`**/${destination.file}`, (route) => { script = route; });
  await page.route(`**/${destination.css![0]}`, (route) => { stylesheet = route; });
  await page.click('a[href="/dashboard/settings"]');
  await expect.poll(() => Boolean(script && stylesheet)).toBe(true);
  await expect(page.locator(progress)).toHaveCount(1);
  await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
  await script!.continue();
  // Vite's CSS preload promise must also settle before the page can mount.
  await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
  await expect(page.locator(progress)).toHaveCount(1);
  await stylesheet!.continue();
  await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');
  await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 1');
  await expect(page.locator(progress)).toHaveCount(0);
  expect(await page.locator('[data-go-page-root] main').evaluate((main) => getComputedStyle(main).maxWidth)).toBe('960px');
});

test('layout download preserves SSR content until destination is ready', async ({ page }) => {
  await page.goto('/about');
  let layout: Route | undefined;
  await page.route(`**/${manifest['app/dashboard/layout.svelte'].file}`, (route) => { layout = route; });
  await page.click('nav[data-go-site-nav] a[href="/dashboard"]');
  await expect.poll(() => Boolean(layout)).toBe(true);
  await expect(page.locator(progress)).toHaveCount(1);
  await expect(page.locator('[data-go-page-about]')).toBeVisible();
  await layout!.continue();
  await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
  await expect(page.locator(progress)).toHaveCount(0);
});

test('overlapping trusted clicks cannot hide newer progress or overwrite its destination', async ({ page }) => {
  await page.goto('/dashboard');
  let first: Route | undefined;
  let second: Route | undefined;
  await page.route('**/dashboard/settings', (route) => { first = route; });
  await page.route('**/about', (route) => { second = route; });
  await page.click('a[href="/dashboard/settings"]');
  await expect(page.locator(progress)).toHaveCount(1);
  await page.click('a[href="/about"]');
  await expect.poll(() => Boolean(second)).toBe(true);
  await first!.continue().catch(() => {}); // aborted request may already be gone
  await expect(page.locator(progress)).toHaveCount(1);
  await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
  await second!.continue();
  await expect(page.locator('[data-go-page-about]')).toBeVisible();
  await expect(page.locator(progress)).toHaveCount(0);
  await expect(page).toHaveURL(/\/about$/);
});

test('Back and Forward show progress for delayed history fetches without adding entries', async ({ page }) => {
  await page.goto('/about');
  await page.click('nav[data-go-site-nav] a[href="/dashboard"]');
  await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
  const length = await page.evaluate(() => history.length);
  for (const [method, path, heading] of [['back', '/about', 'About'], ['forward', '/dashboard', 'Dashboard']] as const) {
    let request: Route | undefined;
    await page.route(`**${path}`, (route) => { request = route; });
    await page.evaluate((direction) => history[direction](), method);
    await expect(page.locator(progress)).toHaveCount(1);
    await request!.continue();
    await expect(page.locator(heading === 'About' ? '[data-go-page-about] h1' : '[data-go-page-root] h1')).toHaveText(heading);
    await expect(page.locator(progress)).toHaveCount(0);
    await page.unroute(`**${path}`);
    expect(await page.evaluate(() => history.length)).toBe(length);
  }
});

for (const kind of ['JavaScript', 'CSS'] as const) test(`${kind} import failure clears progress and falls back to a real document`, async ({ page }) => {
  await page.goto('/dashboard');
  await page.evaluate(() => { (window as any).__navigationDocument = true; });
  const destination = manifest['app/dashboard/settings/page.svelte'];
  const asset = kind === 'JavaScript' ? destination.file : destination.css![0];
  let script: Route | undefined;
  let documentFallback = false;
  page.on('request', (request) => {
    if (request.isNavigationRequest() && new URL(request.url()).pathname === '/dashboard/settings') {
      documentFallback = true;
    }
  });
  await page.route(`**/${asset}`, (route) => {
    if (documentFallback) return route.continue();
    if (script) return route.abort('failed'); // native import can retry a failed modulepreload
    script = route;
  });
  await page.click('a[href="/dashboard/settings"]');
  await expect(page.locator(progress)).toHaveCount(1);
  await script!.abort('failed');
  await expect.poll(() => documentFallback).toBe(true);
  await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');
  expect(await page.evaluate(() => (window as any).__navigationDocument)).toBeUndefined();
  await expect(page.locator(progress)).toHaveCount(0);
});
