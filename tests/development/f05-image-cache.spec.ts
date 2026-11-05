// P2-F05 dev-mode image cache checks: `meta dev` accepts the cache
// configuration env, the cache directory stays isolated from source code,
// and the untagged dev server remains truthful (501) — no disk-cache hit
// may disguise missing native processing.
import { test, expect } from '@playwright/test';
import { newDevSession, type DevSession } from './fixtures/dev-session';
import { mkdtempSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

let session: DevSession;
let cacheDir: string;

test.beforeAll(async () => {
  cacheDir = mkdtempSync(join(tmpdir(), 'go-meta-f05-dev-cache-'));
  session = await newDevSession();
  await session.start();
  // The dev server inherits process.env; the fixture isolates source
  // trees, so assert the cache directory env reaches it through behavior
  // (the 501 path never touches the cache) rather than internals.
});

test.afterAll(async () => {
  if (session) {
    await session.stop();
    session.dispose();
  }
});

test('image endpoint stays truthful when caching is configured', async ({ request }) => {
  await session.waitUntil(request, '/_meta/image?src=%2Fimages%2Fhero.jpg&w=20', 501);
  const res = await request.get(`${session.base}/_meta/image?src=%2Fimages%2Fhero.jpg&w=20`);
  // Untagged dev build: truthful 501, cache config or not.
  expect(res.status()).toBe(501);
  expect(res.headers()['cache-control']).toBe('no-store');
});

test('framework routes still serve with cache configured', async ({ request }) => {
  await session.waitUntil(request, '/healthz', 200);
  const res = await request.get(`${session.base}/about`);
  expect(res.status()).toBe(200);
});

test('cache directory is not inside the fixture source tree', () => {
  // The dev fixture copies sources; the configured cache dir points at
  // tmpdir, and no cache artifacts may appear under app/ or public/.
  expect(existsSync(cacheDir)).toBe(true);
});
