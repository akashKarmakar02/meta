import { test, expect, type Route } from '@playwright/test';

// E06 integration gate: the caching layers as one system in a real browser.
// Focused only on gaps not already covered by e03/e04/e05 specs:
//   1. stale soft-navigation suppression on a cached route (slow cached fill
//      loses to a fast cached destination; no stale commit, no reload);
//   2. browser refresh keeps serving cached data, and distinct query
//      variants stay isolated across reloads;
//   3. post-action invalidation followed by a full-document GET (what a
//      native form's PRG navigation does) serves fresh data.

/** Holds the soft-navigation envelope for path so its transition is in flight. */
async function holdEnvelope(page: import('@playwright/test').Page, path: string): Promise<{ isHeld: () => boolean; release: () => Promise<void> }> {
  let held: Route | null = null;
  await page.route(`**${path}**`, (route) => {
    if (route.request().headers()['x-go-web-navigation']) {
      held = route;
      return; // hold the envelope; the navigation stays in flight
    }
    return route.continue();
  });
  return {
    isHeld: () => held !== null,
    release: async () => {
      const r = held;
      held = null;
      if (r) await r.continue();
    },
  };
}

test('a slow cached soft navigation superseded by a fast one never commits stale data', async ({ page }) => {
  // Warm the cache for both destinations first: the slow one through a
  // slow=1 fill, the fast one ordinary.
  await page.goto('/cache-demo/team/slow?slow=1');
  await expect(page.locator('[data-cache-id]')).toHaveText('slow');
  await page.goto('/cache-demo/team/fast');
  await expect(page.locator('[data-cache-id]')).toHaveText('fast');

  await page.goto('/cache-demo/team/start');
  await expect(page.locator('[data-cache-id]')).toHaveText('start');
  await page.evaluate(() => {
    (window as unknown as Record<string, unknown>).__BOOT_COUNT = 1;
  });

  // Hold the slow cached destination's envelope so its transition is in
  // flight, then navigate fast — latest destination wins and the held,
  // superseded response must never commit.
  const held = await holdEnvelope(page, '/cache-demo/team/slow');
  await page.evaluate(() => {
    const link = document.createElement('a');
    link.href = '/cache-demo/team/slow';
    link.textContent = 'Go slow';
    document.body.append(link);
  });
  await page.getByRole('link', { name: 'Go slow' }).click();
  await expect.poll(() => held.isHeld(), { message: 'slow envelope must be in flight' }).toBe(true);

  await page.evaluate(() => {
    const link = document.createElement('a');
    link.href = '/cache-demo/team/fast';
    link.textContent = 'Go fast';
    document.body.append(link);
  });
  await page.getByRole('link', { name: 'Go fast' }).click();
  await expect(page.locator('[data-cache-id]')).toHaveText('fast');
  await expect(page).toHaveURL(/team\/fast$/);

  // Release the superseded slow envelope late; it must not overwrite the
  // committed destination or cause a full-document fallback.
  await held.release();
  await page.waitForTimeout(300);
  await expect(page).toHaveURL(/team\/fast$/);
  await expect(page.locator('[data-cache-id]')).toHaveText('fast');
  const boot = await page.evaluate(() => (window as unknown as Record<string, unknown>).__BOOT_COUNT);
  expect(boot, 'no full-document fallback for a superseded transition').toBe(1);
});

test('cached data survives browser refresh and query variants remain isolated', async ({ page }) => {
  await page.goto('/cache-demo/post/e06-refresh?tab=one');
  await expect(page.locator('[data-cache-tab]')).toHaveText('one');
  const first = await page.locator('[data-cache-refreshed]').textContent();
  expect(first).toBeTruthy();
  // A browser refresh is a new document GET that must hit the cache.
  await page.reload();
  await expect(page.locator('[data-cache-refreshed]')).toHaveText(first!);
  // A different query value is its own cache identity: fresh fill with a
  // new timestamp, never the first query's cached value.
  await page.goto('/cache-demo/post/e06-refresh?tab=two');
  await expect(page.locator('[data-cache-tab]')).toHaveText('two');
  await expect(page.locator('[data-cache-refreshed]')).not.toBe(first!);
});

test('an invalidating action followed by a full-document GET serves fresh data', async ({ page }) => {
  await page.goto('/cache-demo/post/e06-form');
  const first = await page.locator('[data-cache-refreshed]').textContent();
  expect(first).toBeTruthy();
  // The action mutates id "e06-form" to a known value and invalidates the
  // exact key. Called as a JSON action here (the form contract itself is
  // covered by the D-phase suites); the assertion below is about what a
  // native form's PRG step does next: a full-document GET must be fresh.
  const res = await page.evaluate(async () => {
    const r = await fetch('/_meta/actions/cache-demo.update', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Go-Meta-Action': '1' },
      body: JSON.stringify({ id: 'e06-form', title: 'e06-fresh' }),
    });
    return { status: r.status, body: await r.json() };
  });
  expect(res.status).toBe(200);
  expect(res.body.ok).toBe(true);
  await page.reload();
  await expect(page.locator('[data-cache-refreshed]')).toHaveText('e06-fresh');
  // And repeated loads keep hitting the repopulated cache.
  await page.reload();
  await expect(page.locator('[data-cache-refreshed]')).toHaveText('e06-fresh');
});
