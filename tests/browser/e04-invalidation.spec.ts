import { test, expect } from '@playwright/test';

// E04: action-triggered invalidation makes the next request fresh.
// A cached SSR value is displayed; the cache-demo.update action commits a
// mutation and invalidates; a subsequent SSR GET reloads fresh data. The
// same holds for a CSR route through a soft navigation. Parameter variants
// remain isolated (exact-key invalidation).

test('SSR cache shows fresh data after an action invalidates it', async ({ page }) => {
  await page.goto('/cache-demo/post/42');
  // First visit fills the cache with a timestamp value.
  const first = await page.locator('[data-cache-refreshed]').textContent();
  await page.reload();
  await expect(page.locator('[data-cache-refreshed]')).toHaveText(first!);

  // Action commits a mutation (version "fresh-ssr" for id 42) then
  // invalidates the exact cached key for this route.
  const res = await page.evaluate(async () => {
    const r = await fetch('/_meta/actions/cache-demo.update', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Go-Meta-Action': '1' },
      body: JSON.stringify({ id: '42', title: 'fresh-ssr' }),
    });
    return { status: r.status, body: await r.json() };
  });
  expect(res.status).toBe(200);
  expect(res.body.ok).toBe(true);
  expect(res.body.data.invalidated).toBe(true);

  // The next document GET must miss and reload: committed mutation visible.
  await page.reload();
  await expect(page.locator('[data-cache-refreshed]')).toHaveText('fresh-ssr');
});

test('CSR soft navigation shows fresh data after an action invalidates it', async ({ page }) => {
  await page.goto('/cache-demo/team/alpha');
  await expect(page.locator('[data-cache-id]')).toHaveText('alpha');
  const first = await page.locator('[data-cache-refreshed]').textContent();
  await page.reload();
  await expect(page.locator('[data-cache-refreshed]')).toHaveText(first!);

  const ok = await page.evaluate(async () => {
    const r = await fetch('/_meta/actions/cache-demo.update', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Go-Meta-Action': '1' },
      body: JSON.stringify({ id: 'alpha', title: 'fresh-csr' }),
    });
    return r.status === 200;
  });
  expect(ok).toBe(true);

  // Navigate away first: a plain link to the current URL is a router no-op,
  // so the test must re-enter the route from a different URL.
  await page.evaluate(() => {
    const back = document.createElement('a'); back.href = '/cache-demo/team/alpha'; back.textContent = 'Reopen alpha'; document.body.append(back);
  });
  await page.getByRole('link', { name: 'Home' }).click();
  await expect(page).toHaveURL(/\/$/);
  // A soft navigation to the invalidated key must obtain fresh data.
  await page.getByRole('link', { name: 'Reopen alpha' }).click();
  await expect(page).toHaveURL(/team\/alpha$/);
  await expect(page.locator('[data-cache-id]')).toHaveText('alpha');
  await expect(page.locator('[data-cache-refreshed]')).toHaveText('fresh-csr');

  // Full refresh shows the same fresh value (URL/history unchanged otherwise).
  await page.reload();
  await expect(page.locator('[data-cache-refreshed]')).toHaveText('fresh-csr');
});

test('invalidating one id does not invalidate another', async ({ page }) => {
  await page.goto('/cache-demo/post/42');
  const v42 = await page.locator('[data-cache-refreshed]').textContent();
  await page.goto('/cache-demo/post/99');
  const v99 = await page.locator('[data-cache-refreshed]').textContent();
  // Mutate + invalidate only id 99.
  const ok = await page.evaluate(async () => {
    const r = await fetch('/_meta/actions/cache-demo.update', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Go-Meta-Action': '1' },
      body: JSON.stringify({ id: '99', title: 'fresh-99' }),
    });
    return r.status === 200;
  });
  expect(ok).toBe(true);
  // id 42 keeps its cached entry…
  await page.goto('/cache-demo/post/42');
  await expect(page.locator('[data-cache-refreshed]')).toHaveText(v42!);
  // …while id 99 is fresh.
  await page.goto('/cache-demo/post/99');
  await expect(page.locator('[data-cache-refreshed]')).toHaveText('fresh-99');
});
