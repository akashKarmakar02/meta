// C05 browser tests: loader errors, redirects, and cancellation end-to-end in
// a real browser, against the production binary.
//
// The c05/* fixture routes carry registered loaders that redirect or report an
// error outcome. /c05/source hosts plain same-origin links the tests click
// with a REAL click (isTrusted === true) so the router's soft-navigation
// interceptor engages — a programmatic a.click() is untrusted and would take
// the native full-document path instead.
//
// These checks only a real browser can make:
//   - a loader redirect is followed on a direct load (browser follows the 302);
//   - a loader redirect is followed during soft navigation (fetch follows the
//     same-origin 302 and the target's envelope is applied to the outlet);
//   - a loader error stops the page from rendering (direct 404, and a soft-nav
//     error falls back to a full-document load of the error page);
//   - a STALE redirect can never change the current page: a second navigation
//     started while a slow redirect is in flight supersedes it, and the slow
//     redirect's target is never committed.
import { test, expect, type Page } from '@playwright/test';

const about = (page: Page) => page.locator('[data-go-page-about]');
const dashboard = (page: Page) => page.locator('[data-go-page="dashboard"]');
const source = (page: Page) => page.locator('[data-go-page="c05-source"]');
const notFoundPage = (page: Page) => page.locator('[data-go-error="404"]');

test.describe('C05 loader redirects', () => {
  test('SSR loader redirect is followed on a direct load', async ({ page }) => {
    // The loader for /c05/redirectSsr redirects to /about; the browser follows
    // the 302 transparently, so the final document is the About page.
    await page.goto('/c05/redirectSsr');
    await expect(page).toHaveURL(/\/about$/);
    await expect(about(page)).toBeVisible();
    // The redirect source page never rendered.
    await expect(page.locator('[data-go-page="c05-redirect-ssr"]')).toHaveCount(0);
  });

  test('CSR loader redirect is followed on a direct load', async ({ page }) => {
    // The loader for /c05/redirectCsr redirects to /dashboard; the browser
    // follows the 302 and the dashboard document loads.
    await page.goto('/c05/redirectCsr');
    await expect(page).toHaveURL(/\/dashboard$/);
    await expect(dashboard(page)).toBeVisible();
    await expect(page.locator('[data-go-page="c05-redirect-csr"]')).toHaveCount(0);
  });

  test('SSR loader redirect works during soft navigation', async ({ page }) => {
    await page.goto('/c05/source');
    await expect(source(page)).toBeVisible();

    // Real click → soft navigation. The fetch to /c05/redirectSsr returns a
    // 302; fetch follows it (same-origin, nav header preserved) and the
    // /about envelope is applied to the outlet without a full reload.
    await page.click('[data-go-c05-redirect-ssr]');
    await expect(page).toHaveURL(/\/about$/);
    await expect(about(page)).toBeVisible();
    // The source page was replaced in the outlet (soft nav), not reloaded.
    await expect(source(page)).toHaveCount(0);
  });

  test('CSR loader redirect works during soft navigation', async ({ page }) => {
    await page.goto('/c05/source');
    await expect(source(page)).toBeVisible();

    // Soft navigation to a CSR route whose loader redirects to /dashboard: the
    // fetch follows the 302, the /dashboard envelope is applied, and the CSR
    // page mounts.
    await page.click('[data-go-c05-redirect-csr]');
    await expect(page).toHaveURL(/\/dashboard$/);
    await expect(dashboard(page)).toBeVisible();
  });
});

test.describe('C05 loader errors', () => {
  test('a not-found loader stops rendering on a direct load', async ({ page }) => {
    // The loader for /c05/notFound reports not found; the page never renders
    // and the client receives the 404 error document.
    const resp = await page.goto('/c05/notFound');
    expect(resp?.status()).toBe(404);
    await expect(notFoundPage(page)).toBeVisible();
    await expect(page.locator('[data-go-page="c05-not-found"]')).toHaveCount(0);
  });

  test('a loader error during soft navigation falls back to the error page', async ({
    page,
  }) => {
    await page.goto('/c05/source');
    await expect(source(page)).toBeVisible();

    // Soft navigation to /c05/notFound: the fetch returns 404 (a non-2xx), so
    // the soft pipeline cannot apply an envelope and falls back to a full
    // document load of the destination — the 404 error page. The source page
    // is gone (the document was replaced), and no partial/stale content remains.
    await page.click('[data-go-c05-not-found]');
    await expect(page).toHaveURL(/\/c05\/notFound$/);
    await expect(notFoundPage(page)).toBeVisible();
    await expect(source(page)).toHaveCount(0);
  });
});

test.describe('C05 cancellation', () => {
  test('a stale redirect can never change the current page', async ({ page }) => {
    await page.goto('/c05/source');
    await expect(source(page)).toBeVisible();

    // Start a soft navigation to /c05/slowRedirect, whose loader sleeps ~400ms
    // before redirecting to /docs. While that fetch is still in flight, start
    // a second soft navigation to /about (fast). The second navigation
    // supersedes the first (beginNavigation aborts the first's fetch and issues
    // a newer token), so the first's redirect to /docs must never commit.
    await page.click('[data-go-c05-slow]');
    await page.click('[data-go-c05-target]');

    // The current (second) navigation commits: the page is /about, not /docs.
    await expect(page).toHaveURL(/\/about$/);
    await expect(about(page)).toBeVisible();
    // The stale redirect's target never rendered.
    await expect(page.locator('[data-go-page-docs]')).toHaveCount(0);
  });
});
