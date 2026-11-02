// H10 acceptance (blueprint §7): slow request races, aborted fetch, server
// error fallback — "latest destination wins".
//
// Scope: the coordinator (F11) aborts the previous navigation when a new
// one begins, and the pipeline (F12) only lets the CURRENT token commit —
// stale or intentionally aborted transitions exit as no-ops, and only a
// CURRENT failure takes the full-document fallback. This suite proves those
// guarantees against real Chromium with DETERMINISTIC held requests (Playwright
// route interception that captures the navigation envelope and releases it
// on demand) — no arbitrary timing races.
//
//   A. slow /about (held) + fast /dashboard → latest wins; the late /about
//      response is discarded, no stale history commit, no fallback.
//   B. stale /about released as HTTP 500 after /dashboard completes → no
//      hard reload; the newer page and its document identity survive.
//   C. current navigation answered HTTP 500 → documented full-document
//      fallback to the intended destination.
//   D. current navigation fetch fails (network) → full-document fallback.
//
// Scenario E (superseded layout-subtree exit mid-teardown) is a
// sub-millisecond teardown window that cannot be deterministically driven
// from Playwright input; it is pinned by the G07-hardening unit test
// (commit f84bc35) and is outside H10's exact acceptance criterion.
//
// A trailing test assesses the H09 same-document popstate fix: a real
// cross-route Back still restores, while hash-only movement within one
// document fetches no envelope.
import { test, expect, type Page } from '@playwright/test';

/** Sets the boot counter on the live document; wiped by any real reload. */
async function markBootCount(page: Page): Promise<void> {
  await page.evaluate(() => {
    (window as unknown as Record<string, unknown>).__BOOT_COUNT = 1;
  });
}

/** Reads the boot counter; undefined means the document was reloaded. */
async function bootCount(page: Page): Promise<unknown> {
  return page.evaluate(
    () => (window as unknown as Record<string, unknown>).__BOOT_COUNT
  );
}

/** Counts navigation-envelope fetches (X-Go-Web-Navigation header). */
function trackEnvelopeRequests(page: Page): { count(): number } {
  let n = 0;
  page.on('request', (req) => {
    if (req.headers()['x-go-web-navigation']) n += 1;
  });
  return { count: () => n };
}

/**
 * Holds the navigation envelope for `path` (only the soft-nav request, not
 * the plain document GET) and returns a release handle. The held route is
 * the deterministic synchronization point: the caller knows the navigation
 * is in-flight once `held` is set.
 */
async function holdEnvelope(page: Page, path: string): Promise<{
  isHeld: () => boolean;
  release: (opts: { status?: number; body?: string; contentType?: string } | 'abort') => Promise<void>;
}> {
  let held: import('@playwright/test').Route | null = null;
  await page.route(`**${path}`, (route) => {
    if (route.request().headers()['x-go-web-navigation']) {
      held = route;
      return; // hold — do not continue or fulfill yet
    }
    return route.continue();
  });
  return {
    isHeld: () => held !== null,
    release: async (opts) => {
      if (held === null) throw new Error('no held envelope to release');
      const r = held;
      held = null;
      if (opts === 'abort') {
        await r.abort();
      } else {
        await r.fulfill({
          status: opts.status ?? 200,
          contentType: opts.contentType ?? 'application/json',
          body:
            opts.body ??
            JSON.stringify({
              version: 1,
              url: path,
              routeId: 'about/page.templ',
              mode: 'ssr',
              title: 'About',
              layouts: ['app/layout.templ'],
              html: '<main data-go-page-about><h1>About</h1><p>late</p></main>',
              clientEntry: null,
            }),
        });
      }
    },
  };
}

test.describe('H10: slow request races, aborted fetch, server error fallback', () => {
  test('A: slow /about + fast /dashboard — latest destination wins, no stale commit', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/');
    await expect(page.locator('[data-go-page="home"]')).toBeVisible();
    await markBootCount(page);
    const envelopes = trackEnvelopeRequests(page);

    // Start A: /about — its envelope is held, so the navigation is in flight.
    const held = await holdEnvelope(page, '/about');
    await page.click('a[href="/about"]');
    await expect
      .poll(() => held.isHeld(), { message: '/about envelope must be in flight' })
      .toBe(true);

    // Start B: /dashboard — supersedes A (coordinator aborts A's fetch).
    await page.click('a[href="/dashboard"]');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    expect(page.url()).toContain('/dashboard');
    const urlAfterB = page.url();
    const bootAfterB = await bootCount(page);
    const dashRoots = await page.locator('[data-go-page-root]').count();

    // Release A late as a valid 200 envelope.
    await held.release({});
    await page.waitForTimeout(300);

    // Latest (B) still wins: /about never rendered, no stale history, no
    // fallback, and the document was never reloaded.
    expect(await page.locator('[data-go-page-about]').count(), 'stale /about must not render').toBe(0);
    expect(page.url()).toBe(urlAfterB);
    expect(await bootCount(page), 'stale release must not reload').toBe(bootAfterB);
    expect(await page.locator('[data-go-page-root]').count()).toBe(dashRoots);
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });

  test('B: stale /about released as HTTP 500 after /dashboard — no hard reload', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/');
    await markBootCount(page);
    const envelopes = trackEnvelopeRequests(page);

    const held = await holdEnvelope(page, '/about');
    await page.click('a[href="/about"]');
    await expect
      .poll(() => held.isHeld(), { message: '/about envelope must be in flight' })
      .toBe(true);

    await page.click('a[href="/dashboard"]');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    const urlAfterB = page.url();
    const bootAfterB = await bootCount(page);
    // Two envelope fetches so far: the held /about and the /dashboard.
    const countBeforeRelease = envelopes.count();
    expect(countBeforeRelease).toBe(2);

    // Release the stale /about as a 500 — a stale failure must be a no-op,
    // never a full-document fallback (which would issue another fetch).
    await held.release({ status: 500, body: 'boom' });
    await page.waitForTimeout(300);

    expect(page.url()).toBe(urlAfterB);
    expect(await bootCount(page), 'stale 500 must not hard-reload').toBe(bootAfterB);
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    expect(await page.locator('[data-go-page-about]').count()).toBe(0);
    // The stale 500 was a no-op: no fallback GET, no extra envelope fetch.
    expect(envelopes.count(), 'stale failure must not trigger a fallback fetch').toBe(countBeforeRelease);

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });

  test('C: current navigation answered HTTP 500 — full-document fallback to destination', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/');
    await expect(page.locator('[data-go-page="home"]')).toBeVisible();
    await markBootCount(page);

    // The CURRENT navigation's envelope is a 500.
    await page.route('**/about', (route) => {
      if (route.request().headers()['x-go-web-navigation']) {
        return route.fulfill({ status: 500, contentType: 'application/json', body: 'boom' });
      }
      return route.continue();
    });

    await page.click('a[href="/about"]');
    // The documented fallback is a real full-document navigation to /about.
    await page.waitForURL('**/about');
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    expect(await bootCount(page), 'current 500 must hard-reload').toBeUndefined();

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });

  test('D: current navigation fetch fails (network) — full-document fallback, no partial DOM', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/');
    await expect(page.locator('[data-go-page="home"]')).toBeVisible();
    await markBootCount(page);

    // Abort the CURRENT navigation's envelope fetch (simulated network failure).
    await page.route('**/about', (route) => {
      if (route.request().headers()['x-go-web-navigation']) {
        return route.abort();
      }
      return route.continue();
    });

    await page.click('a[href="/about"]');
    await page.waitForURL('**/about');
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    expect(await bootCount(page), 'network failure must hard-reload').toBeUndefined();
    // The failed envelope must not have partially mutated the DOM: exactly
    // one page outlet, the fresh /about document.
    expect(await page.locator('[data-go-page-outlet]').count()).toBe(1);
    expect(await page.locator('[data-go-page-about]').count()).toBe(1);

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });
});

test.describe('H09 history regression assessment (same-document popstate fix)', () => {
  test('cross-route Back still restores; hash-only movement fetches no envelope', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    // Build a two-entry stack: /docs then /about (soft).
    await page.goto('/docs');
    await expect(page.locator('[data-go-page-docs]')).toBeVisible();
    await markBootCount(page);
    await page.click('a[href="/about"]');
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    expect(page.url()).toContain('/about');

    // Real cross-route Back: /about -> /docs is a genuine route restoration
    // (H09's sameDocument guard must NOT swallow it — path changed).
    await page.goBack();
    await expect(page.locator('[data-go-page-docs]')).toBeVisible();
    expect(page.url()).toContain('/docs');
    expect(await bootCount(page), 'cross-route Back must stay soft').toBe(1);

    // Hash-only movement within the SAME /docs document: no envelope fetch.
    const envelopes = trackEnvelopeRequests(page);
    await page.evaluate(() => {
      const a = document.createElement('a');
      a.id = 'h10-hash';
      a.textContent = 'hash';
      a.href = '#some-section';
      document.body.appendChild(a);
    });
    await page.click('#h10-hash');
    await page.evaluate(
      () => new Promise<void>((r) => requestAnimationFrame(() => requestAnimationFrame(() => r())))
    );
    expect(envelopes.count(), 'hash-only movement must not fetch an envelope').toBe(0);
    expect(await bootCount(page), 'hash movement must not reload').toBe(1);
    // The /docs page is intact — the anchor did not get replaced.
    await expect(page.locator('[data-go-page-docs]')).toBeVisible();

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });
});
