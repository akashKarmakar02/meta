import { test, expect } from '@playwright/test';

test.use({ baseURL: 'http://localhost:3133' });

test('HTML-first cache supports CSR → templ, templ → templ, history and metadata without reload', async ({ page, request }) => {
  // Populate through a document request before the browser's navigation fetch.
  const html = await request.get('/about');
  expect(html.headers()['content-type']).toBe('text/html; charset=utf-8');
  expect(await html.text()).toContain('<title>About</title>');
  const generations = html.headers()['x-test-generations'];
  const nav = await request.get('/about', { headers: { 'X-Go-Web-Navigation': '1' } });
  expect(nav.headers()['content-type']).toBe('application/json');
  expect(nav.headers()['cache-control']).toBe('no-store');
  expect(nav.headers()['x-test-generations']).toBe(generations);
  expect((await nav.json()).metadata.description).toBe('Learn how Meta renders Go and Svelte pages.');

  const errors: string[] = [];
  page.on('pageerror', e => errors.push(String(e)));
  const response = await page.goto('/products/new');
  expect(response?.status()).toBe(200);
  await expect(page.locator('[data-go-page="products-new"]')).toBeVisible();
  await page.evaluate(() => { (window as unknown as Record<string, unknown>).__cachedDocument = true; });
  await page.click('nav a[href="/about"]');
  await expect(page.locator('[data-go-page-about]')).toBeVisible();
  await expect(page).toHaveTitle('About');
  await expect(page.locator('meta[name="description"]')).toHaveAttribute('content', 'Learn how Meta renders Go and Svelte pages.');
  await page.click('nav a[href="/"]');
  await expect(page.locator('[data-go-page="home"]')).toBeVisible();
  await expect(page).toHaveTitle('Meta Framework Project');
  await page.goBack();
  await expect(page).toHaveTitle('About');
  await page.goForward();
  await expect(page.locator('[data-go-page="home"]')).toBeVisible();
  await page.click('nav a[href="/about"]');
  await expect(page).toHaveTitle('About');
  expect(await page.evaluate(() => (window as unknown as Record<string, unknown>).__cachedDocument)).toBe(true);
  const final = await request.get('/about', { headers: { 'X-Go-Web-Navigation': '1' } });
  // Only /about and / were generated; all repeated visits reuse their pairs.
  expect(Number(final.headers()['x-test-generations'])).toBe(Number(generations) + 1);
  expect(errors).toEqual([]);
  const scripts = await page.locator('script[type="module"][src]').evaluateAll(nodes => nodes.map(n => (n as HTMLScriptElement).src));
  expect(scripts.length).toBeGreaterThan(0);
  for (const src of scripts) expect((await request.get(src)).status()).toBe(200);
});

test('navigation-first pair supplies a complete document and assets on later direct navigation', async ({ page, request }) => {
  const path = '/about?cache-order=navigation-first';
  const nav = await request.get(path, { headers: { 'X-Go-Web-Navigation': '1' } });
  expect(nav.headers()['content-type']).toBe('application/json');
  expect((await nav.json()).url).toBe(path);
  const response = await page.goto(path);
  expect(response?.headers()['content-type']).toBe('text/html; charset=utf-8');
  expect(response?.headers()['x-test-generations']).toBe(nav.headers()['x-test-generations']);
  await expect(page).toHaveTitle('About');
  await expect(page.locator('[data-go-page-about]')).toBeVisible();
  await page.click('nav a[href="/products/new"]');
  await expect(page.locator('[data-go-page="products-new"]')).toBeVisible();
});
