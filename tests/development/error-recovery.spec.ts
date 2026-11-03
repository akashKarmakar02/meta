// Error recovery (P2-A06 §13): Go and templ compile errors surface as
// readable diagnostics without killing the session, and corrections
// recover automatically.
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

test('Go compilation error is reported and recovers', async ({ request }) => {
  const backend = 'cmd/example/main.go';
  const original = read(session.root, backend);
  // Introduce a compile error.
  write(session.root, backend, original + '\nfunc init() { undefinedSymbol() }');
  // Air prints the build failure; meta dev stays running.
  await expect.poll(() => session.output.includes('undefinedSymbol') || session.output.includes('undefined: undefinedSymbol'), { timeout: 60_000 }).toBe(true);
  // The old binary keeps serving (Air does not replace a working binary
  // with a broken build).
  await expect.poll(async () => { try { return (await request.get(`${session.base}/healthz`, { timeout: 1000 })).status(); } catch { return 0; } }, { timeout: 10_000 }).toBe(200);
  // Fix it; the session recovers.
  write(session.root, backend, original);
  await expect.poll(async () => { try { return await (await request.get(`${session.base}/healthz`, { timeout: 1000 })).text(); } catch { return ''; } }, { timeout: 60_000 }).toBe('ok');
});

test('templ syntax error produces diagnostics and recovers', async ({ request }) => {
  const nav = 'app/nav.templ';
  const original = read(session.root, nav);
  write(session.root, nav, original + '\nunclosed templ expression {{{{');
  await expect.poll(() => /error|Error|failed/.test(session.output), { timeout: 60_000 }).toBe(true);
  write(session.root, nav, original);
  await expect.poll(async () => { try { return (await request.get(`${session.base}/healthz`, { timeout: 1000 })).text(); } catch { return ''; } }, { timeout: 60_000 }).toBe('ok');
});
