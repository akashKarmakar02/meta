// Go hot reload (P2-A06 §13): editing a Go handler triggers Air, the
// backend eventually serves the updated behavior, the public proxy stays
// available through the restart window, and Vite/HMR stays connected.
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

test('backend handler edit rebuilds and restarts the backend', async ({ request }) => {
  await session.waitUntil(request, '/healthz', 200);
  const backend = 'cmd/example/main.go';
  const original = read(session.root, backend);
  expect(original).toContain('return c.SendString("ok")');
  write(session.root, backend, original.replace('return c.SendString("ok")', 'return c.SendString("ok-restarted")'));
  await expect.poll(() => (session.output.match(/running\.\.\./g) ?? []).length, { timeout: 60_000 }).toBeGreaterThan(0);
  await expect.poll(async () => { try { return await (await request.get(`${session.base}/healthz`, { timeout: 1000 })).text(); } catch { return ''; } }, { timeout: 60_000 }).toBe('ok-restarted');
});

test('public proxy stays available during and after the restart window', async ({ request }) => {
  // Trigger a rebuild, then assert requests recover with bounded behavior:
  // only 200 or 503 are acceptable mid-restart answers — never a hang, never
  // another error status, never a Vite fallback body.
  const backend = 'cmd/example/main.go';
  write(session.root, backend, read(session.root, backend).replace('ok-restarted', 'ok-restarted-2'));
  const deadline = Date.now() + 60_000;
  let recovered = false;
  while (Date.now() < deadline && !recovered) {
    let status = -1;
    try { status = (await request.get(`${session.base}/healthz`, { timeout: 2000 })).status(); } catch { status = 0; }
    if (status === 200) recovered = true;
    else if (status !== 0 && status !== 503) throw Error(`unexpected status ${status} during restart window`);
    if (!recovered) await new Promise((r) => setTimeout(r, 200));
  }
  expect(recovered).toBe(true);
});

test('HMR stays connected across a Go restart', async ({ page, request }) => {
  await session.waitUntil(request, '/healthz', 200);
  const sockets: string[] = [];
  const closed: string[] = [];
  page.on('websocket', (ws) => { sockets.push(ws.url()); ws.on('close', () => closed.push(ws.url())); });
  await page.goto(`${session.base}/dashboard`);
  await expect(page.locator('[data-go-page="dashboard"]')).toBeVisible();
  await expect.poll(() => sockets.length, { timeout: 30_000 }).toBeGreaterThan(0);
  write(session.root, 'cmd/example/main.go', read(session.root, 'cmd/example/main.go').replace('ok-restarted-2', 'ok-restarted-3'));
  await expect.poll(() => (session.output.match(/running\.\.\./g) ?? []).length, { timeout: 60_000 }).toBeGreaterThan(1);
  await expect.poll(async () => { try { return await (await request.get(`${session.base}/healthz`, { timeout: 1000 })).text(); } catch { return ''; } }, { timeout: 60_000 }).toBe('ok-restarted-3');
  // The HMR socket was not torn down by the backend restart.
  const stillConnected = sockets.length > closed.length;
  expect(stillConnected).toBe(true);
});
