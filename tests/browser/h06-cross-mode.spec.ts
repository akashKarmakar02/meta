// H06 acceptance (blueprint §7): cross-mode clicks do not reload the
// document — proven by a document-lifetime indicator (window.__BOOT_COUNT,
// set from the test; a full document load wipes it, a soft navigation
// keeps it).
//
// Scope guard: the G05–G08 specs already prove no-reload behavior for the
// dashboard subtree (child swaps, return navigation, exits). This file
// covers what H06 owns and they do not:
//
//   - SSR → SSR soft clicks through the persistent templ nav (/ → /about
//     → /docs): the page outlet swaps, everything else survives.
//   - SSR → CSR cross-mode entry (/about → /dashboard): layout subtree
//     mounts client-side, destination page is interactive afterwards.
//   - The framework's ONE intentional hard-navigation fallback: a
//     directly-loaded bare CSR shell has no [data-go-page-outlet], so a
//     click to an SSR route correctly performs a full document navigation
//     (the runtime never fabricates server layouts) — asserted as a
//     reload, so a regression toward silent breakage cannot pass as soft.
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
  test('SSR / → /about → /docs: outlet swap stays soft, nav survives, titles update', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/');
    await expect(page.locator('[data-go-page="home"]')).toBeVisible();

    // Document-lifetime markers: the boot counter and a stamped persistent
    // nav element. Both survive only if the document survives.
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
    // The old page is gone; the persistent nav is the SAME element.
    expect(await page.locator('[data-go-page="home"]').count()).toBe(0);
    expect(
      await page.locator('nav[data-go-site-nav][data-e2e-nav-id="persists"]').count()
    ).toBe(1);

    // A second soft hop, SSR → SSR again: /docs.
    await page.click('nav[data-go-site-nav] a[href="/docs"]');
    await expect(page.locator('[data-go-page-docs]')).toBeVisible();
    expect(page.url()).toContain('/docs');
    await expect(page).toHaveTitle('Docs');
    expect(await bootCount(page), '/docs must arrive without a reload').toBe(1);
    expect(
      await page.locator('nav[data-go-site-nav][data-e2e-nav-id="persists"]').count()
    ).toBe(1);
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

  test('direct-loaded CSR shell → SSR route takes the documented full-document fallback', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    // Direct load of a bare CSR shell — /products/new boots without any
    // client layout, so its own SiteNav renders standalone and gives the
    // test a trusted link to click. The shell has no server layout, no
    // outlet; /dashboard's shell behaves identically but hides its nav
    // behind the mounting layout.
    await page.goto('/products/new');
    await expect(page.locator('[data-go-page="products-new"]')).toBeVisible();
    expect(await page.locator('[data-go-page-outlet]').count()).toBe(0);
    expect(await page.locator('[data-go-layout]').count()).toBe(0);
    await markBootCount(page);

    // Structurally unsupported transition (no outlet to render the SSR
    // fragment into): the router must fall back to a real navigation, not
    // strand the user — and the test must not mistake that for soft nav.
    await page.click('nav[data-go-site-nav] a[href="/about"]');
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    expect(page.url()).toContain('/about');

    // Full document load: the marker was wiped by the navigation.
    expect(await bootCount(page), 'fallback must be a real document load').toBeUndefined();

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });
});
