// Development boot (P2-A06 §13): the public origin serves SSR and CSR
// pages, and every browser request — modules and HMR WebSockets included —
// uses that single origin, never the internal Vite port.
import { test, expect } from '@playwright/test';
import { newDevSession, type DevSession } from './fixtures/dev-session';

let session: DevSession;

test.beforeAll(async () => {
  session = await newDevSession();
  await session.start();
});

test.afterAll(async () => {
  await session.stop();
  session.dispose();
});

test('public origin is reachable and answers the health endpoint', async ({ request }) => {
  await session.waitUntil(request, '/healthz', 200);
});

test('SSR page renders through the public origin', async ({ request }) => {
  await session.waitUntil(request, '/healthz', 200);
  const response = await request.get(`${session.base}/about`);
  expect(response.status()).toBe(200);
  const html = await response.text();
  expect(html).toContain('About');
});

test('CSR page mounts Svelte from the public origin', async ({ page, request }) => {
  await session.waitUntil(request, '/healthz', 200);
  const browserRequests: string[] = [];
  page.on('request', (req) => { if (['script', 'stylesheet', 'fetch'].includes(req.resourceType())) browserRequests.push(req.url()); });
  await page.goto(`${session.base}/dashboard`);
  await expect(page.locator('[data-go-page="dashboard"]')).toBeVisible();
  // Every module/script/fetch request must use the public origin.
  expect(browserRequests.length).toBeGreaterThan(0);
  for (const url of browserRequests) {
    expect(new URL(url).origin).toBe(session.base);
  }
  // And none may bypass the proxy to the internal Vite port.
  for (const url of browserRequests) {
    expect(new URL(url).port).not.toBe(String(session.vitePort));
  }
});

test('HMR WebSocket connects through the public origin', async ({ page, request }) => {
  await session.waitUntil(request, '/healthz', 200);
  const sockets: string[] = [];
  page.on('websocket', (ws) => sockets.push(ws.url()));
  await page.goto(`${session.base}/dashboard`);
  await expect.poll(() => sockets.length, { timeout: 30_000 }).toBeGreaterThan(0);
  for (const url of sockets) {
    expect(url.startsWith(`${session.base.replace('http', 'ws')}/_go_meta_vite/`)).toBe(true);
  }
});
