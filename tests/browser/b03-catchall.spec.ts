// B03 acceptance (blueprint §7): catch-all route segments in a real browser.
//
// These are the checks only Chromium can make against the production binary +
// Vite bundle: SSR catch-all rendering of the captured value, CSR catch-all
// mounting by stable routeId with the concrete path read at mount time,
// same-routeId sibling navigation, cross-mode soft navigation, history,
// query strings, no-reload, layout retention, disposal, rapid/stale
// navigation, and the URL-safety / precedence invariants.
//
// SSR catch-all example: /docs/[...slug]  -> <p data-catchall-slug>
// CSR catch-all example: /products/[...path] -> <p data-catchall-path>
// Static under the catch-all: /docs/intro -> [data-static-docs]
import { test, expect, type Page } from '@playwright/test';

/** Sets the document-lifetime marker; a full reload wipes it. */
async function markBootCount(page: Page): Promise<void> {
  await page.evaluate(() => {
    (window as unknown as Record<string, unknown>).__BOOT_COUNT = 1;
  });
}

async function bootCount(page: Page): Promise<unknown> {
  return page.evaluate(
    () => (window as unknown as Record<string, unknown>).__BOOT_COUNT
  );
}

test.describe('B03: catch-all route segments', () => {
  test('1. SSR catch-all direct load renders the captured multi-segment value', async ({ page }) => {
    await page.goto('/docs/getting-started/installation');
    await expect(page.locator('[data-go-page-docs-slug]')).toBeVisible();
    await expect(page.locator('[data-catchall-slug]')).toHaveText(
      'getting-started/installation'
    );
    // Nearest-layout-wins: the docs templ layout wraps the catch-all page.
    await expect(page.locator('[data-go-layout="app/docs/layout.templ"]')).toHaveCount(1);
  });

  test('2. CSR catch-all direct load renders the captured value via stable routeId', async ({ page }) => {
    await page.goto('/products/reports/2026/summary.pdf');
    await expect(page.locator('[data-go-page-products-path]')).toBeVisible();
    await expect(page.locator('[data-catchall-path]')).toHaveText(
      '/products/reports/2026/summary.pdf'
    );
    await expect(page.locator('[data-catchall-segments]')).toHaveText(
      'reports > 2026 > summary.pdf'
    );
    // Mounted by the declared module, not the concrete URL.
    await expect(page.locator('[data-go-page-root]')).toHaveAttribute(
      'data-go-route-id',
      'products/[...path]/page.svelte'
    );
  });

  test('3. deep-link reload of a CSR catch-all URL keeps the value', async ({ page }) => {
    await page.goto('/products/reports/2026/summary.pdf');
    await expect(page.locator('[data-catchall-path]')).toHaveText(
      '/products/reports/2026/summary.pdf'
    );
    await page.reload();
    await expect(page.locator('[data-catchall-path]')).toHaveText(
      '/products/reports/2026/summary.pdf'
    );
    await expect(page.locator('[data-go-page-root]')).toHaveAttribute(
      'data-go-route-id',
      'products/[...path]/page.svelte'
    );
  });

  test('4. cross-mode SSR catch-all -> CSR catch-all is a soft navigation', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(String(e)));
    await page.goto('/docs/getting-started/installation');
    await expect(page.locator('[data-catchall-slug]')).toHaveText(
      'getting-started/installation'
    );
    await markBootCount(page);

    await page.click('a[href="/products/reports/2026/summary.pdf"]');
    await expect(page.locator('[data-catchall-path]')).toHaveText(
      '/products/reports/2026/summary.pdf'
    );
    expect(page.url()).toContain('/products/reports/2026/summary.pdf');
    expect(await bootCount(page), 'cross-mode entry must not reload').toBe(1);
    expect(await page.locator('[data-go-page-docs-slug]').count()).toBe(0);
    expect(await page.locator('[data-go-page-root]').count()).toBe(1);
    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });

  test('5. cross-mode CSR catch-all -> SSR is a soft navigation', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(String(e)));
    await page.goto('/products/reports/2026/summary.pdf');
    await expect(page.locator('[data-catchall-path]')).toHaveText(
      '/products/reports/2026/summary.pdf'
    );
    await markBootCount(page);

    await page.click('a[href="/about"]');
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    expect(page.url()).toContain('/about');
    expect(await bootCount(page), 'exit to SSR must not reload').toBe(1);
    expect(await page.locator('[data-go-page-products-path]').count()).toBe(0);
    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });

  test('6. sibling navigation (same routeId) updates the captured value', async ({ page }) => {
    await page.goto('/products/reports/2026/summary.pdf');
    await expect(page.locator('[data-catchall-path]')).toHaveText(
      '/products/reports/2026/summary.pdf'
    );

    // Same routeId, deeper path — the page remounts with the new value.
    await page.click('a[href="/products/reports/2026/summary.pdf/annotations"]');
    await expect(page.locator('[data-catchall-path]')).toHaveText(
      '/products/reports/2026/summary.pdf/annotations'
    );
    await expect(page.locator('[data-catchall-segments]')).toHaveText(
      'reports > 2026 > summary.pdf > annotations'
    );

    // Sibling, different first segment (two segments: the catch-all, not the
    // single-segment /products/[id] dynamic route).
    await page.click('a[href="/products/other/doc"]');
    await expect(page.locator('[data-catchall-path]')).toHaveText('/products/other/doc');
    await expect(page.locator('[data-catchall-segments]')).toHaveText('other > doc');
    // Still the same declared route.
    await expect(page.locator('[data-go-page-root]')).toHaveAttribute(
      'data-go-route-id',
      'products/[...path]/page.svelte'
    );
  });

  test('7. back/forward through catch-all history restores the correct value', async ({ page }) => {
    await page.goto('/products/reports/2026/summary.pdf');
    await expect(page.locator('[data-catchall-path]')).toHaveText(
      '/products/reports/2026/summary.pdf'
    );
    await page.click('a[href="/products/reports/2026/summary.pdf/annotations"]');
    await expect(page.locator('[data-catchall-path]')).toHaveText(
      '/products/reports/2026/summary.pdf/annotations'
    );

    await page.goBack();
    await expect(page.locator('[data-catchall-path]')).toHaveText(
      '/products/reports/2026/summary.pdf'
    );
    await page.goForward();
    await expect(page.locator('[data-catchall-path]')).toHaveText(
      '/products/reports/2026/summary.pdf/annotations'
    );
    expect(await page.locator('[data-go-page-root]').count()).toBe(1);
  });

  test('8. query string on a catch-all URL is preserved and not part of the value', async ({ page }) => {
    await page.goto('/products/reports/2026/summary.pdf?tab=1');
    await expect(page.locator('[data-catchall-path]')).toHaveText(
      '/products/reports/2026/summary.pdf'
    );
    await expect(page.locator('[data-catchall-segments]')).toHaveText(
      'reports > 2026 > summary.pdf'
    );
    expect(page.url()).toContain('tab=1');
  });

  test('9. no-reload soft navigation between SSR catch-all pages', async ({ page }) => {
    await page.goto('/docs/getting-started/installation');
    await expect(page.locator('[data-catchall-slug]')).toHaveText(
      'getting-started/installation'
    );
    await markBootCount(page);

    // Same routeId, deeper path: a soft SSR re-render, not a reload.
    await page.click('a[href="/docs/getting-started/installation/linux"]');
    await expect(page.locator('[data-catchall-slug]')).toHaveText(
      'getting-started/installation/linux'
    );
    expect(page.url()).toContain('/docs/getting-started/installation/linux');
    expect(await bootCount(page), 'SSR catch-all sibling must not reload').toBe(1);
    // The docs templ layout persists across the sibling swap.
    await expect(page.locator('[data-go-layout="app/docs/layout.templ"]')).toHaveCount(1);
  });

  test('10. a single page root and no duplicate layout subtrees survive catch-all navigation', async ({ page }) => {
    await page.goto('/products/reports/2026/summary.pdf');
    await expect(page.locator('[data-go-layout="app/layout.templ"]')).toHaveCount(1);
    await expect(page.locator('[data-go-page-root]')).toHaveCount(1);

    await page.click('a[href="/products/reports/2026/summary.pdf/annotations"]');
    await expect(page.locator('[data-catchall-path]')).toHaveText(
      '/products/reports/2026/summary.pdf/annotations'
    );
    // The catch-all swap leaves exactly one page root and one root layout
    // subtree — no duplicate layout subtrees or orphaned page roots.
    await expect(page.locator('[data-go-layout="app/layout.templ"]')).toHaveCount(1);
    await expect(page.locator('[data-go-page-root]')).toHaveCount(1);
    await expect(page.locator('[data-go-page-products-path]')).toHaveCount(1);
  });

  test('11. leaving a catch-all route disposes the CSR page', async ({ page }) => {
    await page.goto('/products/reports/2026/summary.pdf');
    await expect(page.locator('[data-go-page-products-path]')).toBeVisible();
    await page.click('a[href="/about"]');
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    expect(await page.locator('[data-go-page-products-path]').count()).toBe(0);
    expect(await page.locator('[data-catchall-path]').count()).toBe(0);
  });

  test('12. rapid navigation to catch-all URLs lands on the last target with no leak', async ({ page }) => {
    await page.goto('/products/reports/2026/summary.pdf');
    // Fire a burst of same-routeId navigations without waiting between them.
    const links = [
      'a[href="/products/reports/2026/summary.pdf/annotations"]',
      'a[href="/products/other/doc"]',
      'a[href="/products/reports/2026/summary.pdf"]',
    ];
    for (const sel of links) {
      await page.click(sel, { noWaitAfter: true });
    }
    await expect(page.locator('[data-catchall-path]')).toHaveText(
      '/products/reports/2026/summary.pdf'
    );
    expect(await page.locator('[data-go-page-root]').count()).toBe(1);
    expect(await page.locator('[data-go-page-products-path]').count()).toBe(1);
  });

  test('13. malformed catch-all URL (encoded slash) is rejected', async ({ page }) => {
    const res = await page.goto('/docs/a%2Fb');
    expect(res?.status()).toBe(400);
    // The catch-all page must not render a value that smuggled a separator.
    expect(await page.locator('[data-catchall-slug]').count()).toBe(0);
  });

  test('14. zero-segment /docs is the static index, not the catch-all', async ({ page }) => {
    await page.goto('/docs');
    await expect(page.locator('[data-go-page-docs]')).toBeVisible();
    expect(await page.locator('[data-catchall-slug]').count()).toBe(0);
    expect(await page.locator('[data-go-page-docs-slug]').count()).toBe(0);
  });

  test('15. static /docs/intro is not shadowed by the catch-all', async ({ page }) => {
    await page.goto('/docs/intro');
    await expect(page.locator('[data-go-page-docs-intro]')).toBeVisible();
    await expect(page.locator('[data-static-docs]')).toBeVisible();
    expect(await page.locator('[data-catchall-slug]').count()).toBe(0);
    // A real catch-all URL still resolves to the catch-all.
    await page.goto('/docs/getting-started/installation');
    await expect(page.locator('[data-catchall-slug]')).toHaveText(
      'getting-started/installation'
    );
  });

  test('16. a stale catch-all response is discarded when superseded', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(String(e)));

    // Hold the envelope for the deeper catch-all URL so its navigation is
    // in flight, then supersede it with a different catch-all URL.
    let held: import('@playwright/test').Route | null = null;
    await page.route('**/products/reports/2026/summary.pdf/annotations', (route) => {
      if (route.request().headers()['x-go-web-navigation']) {
        held = route;
        return; // hold
      }
      return route.continue();
    });

    await page.goto('/products/reports/2026/summary.pdf');
    await expect(page.locator('[data-catchall-path]')).toHaveText(
      '/products/reports/2026/summary.pdf'
    );
    await markBootCount(page);

    // Start the held navigation (deeper path).
    await page.click('a[href="/products/reports/2026/summary.pdf/annotations"]');
    await expect
      .poll(() => held !== null, { message: 'deeper catch-all envelope in flight' })
      .toBe(true);

    // Supersede it with a sibling catch-all navigation (two segments).
    await page.click('a[href="/products/other/doc"]');
    await expect(page.locator('[data-catchall-path]')).toHaveText('/products/other/doc');
    const urlAfter = page.url();
    const bootAfter = await bootCount(page);

    // Release the stale deeper-path envelope late.
    if (held) await held.fulfill({ status: 200, contentType: 'application/json', body: '{}' });
    await page.waitForTimeout(300);

    // Latest (sibling) still wins: the stale deeper value never rendered, no
    // reload, no stale history commit.
    await expect(page.locator('[data-catchall-path]')).toHaveText('/products/other/doc');
    expect(page.url()).toBe(urlAfter);
    expect(await bootCount(page), 'stale catch-all release must not reload').toBe(bootAfter);
    expect(await page.locator('[data-go-page-root]').count()).toBe(1);
    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });
});
