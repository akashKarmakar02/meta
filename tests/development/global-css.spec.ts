// Verify the example's Vite stylesheet is usable before JavaScript runs and
// remains hot-reloadable. The session is an isolated copy of the repository.
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

test('SSR has a working stylesheet with JavaScript disabled', async ({ browser, request }) => {
  await session.waitUntil(request, '/healthz', 200);
  const context = await browser.newContext({ javaScriptEnabled: false });
  try {
    const page = await context.newPage();
    await page.goto(`${session.base}/`);
    await expect(page.locator('head link[rel="stylesheet"]')).toHaveAttribute('href', '/_go_meta_vite/frontend/app.css?direct');
    await expect(page.locator('[data-go-page="home"] h1')).toHaveCSS('color', 'rgb(37, 99, 235)');
  } finally {
    await context.close();
  }
});

test('CSS edits update without a document reload', async ({ page, request }) => {
  await session.waitUntil(request, '/healthz', 200);
  const sockets: string[] = [];
  page.on('websocket', (ws) => sockets.push(ws.url()));
  await page.goto(`${session.base}/`);
  await expect(page.locator('[data-go-page="home"] h1')).toHaveCSS('color', 'rgb(37, 99, 235)');
  await expect.poll(() => sockets.length).toBeGreaterThan(0);
  await page.evaluate(() => { (window as unknown as Record<string, unknown>).__cssTest = 'retained'; });
  const css = read(session.root, 'frontend/app.css');
  write(session.root, 'frontend/app.css', css.replace('37, 99, 235', '220, 38, 38'));
  await expect(page.locator('[data-go-page="home"] h1')).toHaveCSS('color', 'rgb(220, 38, 38)', { timeout: 30_000 });
  expect(await page.evaluate(() => (window as unknown as Record<string, unknown>).__cssTest)).toBe('retained');
  await expect(page.locator('head link[rel="stylesheet"]')).toHaveCount(1);
});
