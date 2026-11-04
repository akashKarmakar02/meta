import { test, expect } from '@playwright/test';

test('simultaneous SSR and CSR page loads share their respective cache fills', async ({ browser }) => {
  const context = await browser.newContext();
  try {
    for (const kind of ['post', 'team']) {
      const a = await context.newPage();
      const b = await context.newPage();
      const url = `http://localhost:3131/cache-demo/${kind}/e05-${kind}`;
      // Different URL strings prevent browser request deduplication while
      // exercising the same canonical loader cache identity.
      await Promise.all([a.goto(`${url}?slow=1&key=shared`), b.goto(`${url}?key=shared&slow=1`)]);
      const first = a.locator('[data-cache-refreshed]');
      await expect(first).not.toBeEmpty();
      await expect(b.locator('[data-cache-refreshed]')).toHaveText((await first.textContent())!);
      await Promise.all([a.close(), b.close()]);
    }
  } finally {
    await context.close();
  }
});
