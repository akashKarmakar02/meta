// H06 acceptance (blueprint §7): cross-mode clicks do not reload the
// document — proven by a document-lifetime indicator (window.__BOOT_COUNT,
// set from the test; a full document load wipes it, a soft navigation
// keeps it).
//
// Scope guard: the G05–G08 specs already prove no-reload behavior for the
// dashboard subtree (child swaps, return navigation, exits). This file
// covers what H06 owns and they do not:
//
//   - SSR → SSR soft clicks (/ → /about → /docs): visual layouts change
//     while the document survives.
//   - SSR → CSR cross-mode entry (/about → /dashboard): layout subtree
//     mounts client-side, destination page is interactive afterwards.
//   - A direct CSR page with a templ layout can soft-navigate to SSR.
//
// All clicks are trusted Playwright input events against the production
// binary + Vite assets; no synthetic .click() dispatch.
import { test, expect } from '@playwright/test';

/** Sets the boot counter on the live document; wiped by any reload. */
async function markBootCount(page: import('@playwright/test').Page): Promise<void> {
  await page.evaluate(() => {
    (window as unknown as Record<string, unknown>).__BOOT_COUNT = 1;
  });
}

/** Reads the boot counter; undefined means the document was reloaded. */
async function bootCount(page: import('@playwright/test').Page): Promise<unknown> {
  return page.evaluate(
    () => (window as unknown as Record<string, unknown>).__BOOT_COUNT
  );
}

test.describe('H06: cross-mode clicks do not reload the document', () => {
  test('SSR / → /about → /docs: visual layouts change softly and titles update', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/');
    await expect(page.locator('[data-go-page="home"]')).toBeVisible();

    // The document marker survives, while visual-layout markup is replaced.
    await markBootCount(page);
    await page.evaluate(() => {
      document
        .querySelector('nav[data-go-site-nav]')
        ?.setAttribute('data-e2e-nav-id', 'persists');
    });

    // Soft click to /about.
    await page.click('nav[data-go-site-nav] a[href="/about"]');
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    expect(page.url()).toContain('/about');
    await expect(page).toHaveTitle('About');
    expect(await bootCount(page), '/about must arrive without a reload').toBe(1);
    // The old page and old visual-layout markup are gone.
    expect(await page.locator('[data-go-page="home"]').count()).toBe(0);
    expect(
      await page.locator('nav[data-go-site-nav][data-e2e-nav-id="persists"]').count()
    ).toBe(0);
    await expect(page.locator('nav[data-go-site-nav]')).toHaveCount(1);

    // A second soft hop, SSR → SSR again: /docs.
    await page.click('nav[data-go-site-nav] a[href="/docs"]');
    await expect(page.locator('[data-go-page-docs]')).toBeVisible();
    expect(page.url()).toContain('/docs');
    await expect(page).toHaveTitle('Docs');
    expect(await bootCount(page), '/docs must arrive without a reload').toBe(1);
    expect(
      await page.locator('nav[data-go-site-nav][data-e2e-nav-id="persists"]').count()
    ).toBe(0);
    await expect(page.locator('[data-go-layout="app/docs/layout.templ"]')).toHaveCount(1);
    expect(await page.locator('[data-go-page-about]').count()).toBe(0);

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });

  test('SSR /about → CSR /dashboard: cross-mode click stays soft, destination is interactive', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/about');
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    await markBootCount(page);

    // The cross-mode click under test: SSR document → CSR route.
    await page.click('nav[data-go-site-nav] a[href="/dashboard"]');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    expect(page.url()).toContain('/dashboard');
    await expect(page).toHaveTitle('Dashboard');
    expect(await bootCount(page), 'cross-mode entry must not reload').toBe(1);

    // The SSR page was replaced by the client-mounted page root.
    expect(await page.locator('[data-go-page-about]').count()).toBe(0);
    expect(await page.locator('[data-go-page-root]').count()).toBe(1);

    // Interactivity after CSR mount: the page button and the persistent
    // layout counter both respond to real clicks. (exact: the layout
    // counter button is named "Increment layout counter".)
    await page.getByRole('button', { name: 'Increment', exact: true }).click();
    await page.getByRole('button', { name: 'Increment', exact: true }).click();
    await expect(page.locator('main[data-go-page="dashboard"] p')).toHaveText('Count: 2');
    await page.click('[data-go-layout-counter-button]');
    await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 1');

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });

  test('direct CSR page with templ layout → SSR route stays soft', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    // The selected root templ layout supplies the site navigation.
    await page.goto('/products/new');
    await expect(page.locator('[data-go-page="products-new"]')).toBeVisible();
    expect(await page.locator('[data-go-page-outlet]').count()).toBe(1);
    expect(await page.locator('[data-go-layout="app/layout.templ"]').count()).toBe(1);
    await markBootCount(page);

    // The document outlet also allows a soft transition back to SSR.
    await page.click('nav[data-go-site-nav] a[href="/about"]');
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    expect(page.url()).toContain('/about');

    // The document-lifetime marker survives.
    expect(await bootCount(page), 'transition remains in the same document').toBe(1);

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });
});
