// H09 acceptance (blueprint §7): modified click, external, download,
// fragment-only — native browser behavior.
//
// Scope guard: F05's unit tests pin the router's eligibility DECISIONS;
// this suite proves them against real Chromium input events — modifier
// and middle clicks open tabs/windows, target=_blank / download /
// external-origin / data-no-spa / rel=external links keep the browser's
// own semantics, and fragment-only links scroll natively without a
// navigation-envelope fetch. The one eligible path (plain primary click)
// must still soft-navigate.
//
// Fixture anchors are injected into the live document (test fixtures, not
// production markup): the interceptor is document-level, so real anchors
// anywhere in the DOM exercise it identically. Every click is a trusted
// Playwright input event; new tabs are observed browser-natively via
// context 'page' events, downloads via 'download' events.
import { test, expect, type Page } from '@playwright/test';

/** Sets the boot counter on the live document; wiped by any reload. */
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

/**
 * Tracks navigation-envelope fetches (X-Go-Web-Navigation header) so tests
 * can prove a click was NOT handed to the soft-navigation pipeline.
 */
function trackEnvelopeRequests(page: Page): { count(): number } {
  let n = 0;
  page.on('request', (req) => {
    if (req.headers()['x-go-web-navigation']) n += 1;
  });
  return { count: () => n };
}

/** Injects one fixture anchor into the document body; returns selector. */
async function injectAnchor(page: Page, id: string, attrs: Record<string, string>): Promise<string> {
  await page.evaluate(([id, attrs]) => {
    const a = document.createElement('a');
    a.id = id as string;
    a.textContent = id as string;
    for (const [k, v] of Object.entries(attrs as Record<string, string>)) {
      a.setAttribute(k, v);
    }
    document.body.appendChild(a);
  }, [id, attrs] as const);
  return `#${id}`;
}

test.describe('H09: native link behavior and interceptor exclusions', () => {
  test('plain primary click still soft-navigates', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/about');
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    await markBootCount(page);

    const sel = await injectAnchor(page, 'h09-normal', { href: '/docs' });
    await page.click(sel);
    await expect(page.locator('[data-go-page-docs]')).toBeVisible();
    expect(page.url()).toContain('/docs');
    expect(await bootCount(page), 'eligible click must stay soft').toBe(1);

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });

  for (const tc of [
    { name: 'ctrl-click', opts: { modifiers: ['Control'] as ('Control')[] } },
    { name: 'shift-click', opts: { modifiers: ['Shift'] as ('Shift')[] } },
    { name: 'middle-click', opts: { button: 'middle' as const } },
  ]) {
    test(`${tc.name} opens a new browsing context and leaves the original untouched`, async ({ page, context }) => {
      const errors: string[] = [];
      page.on('pageerror', (err) => errors.push(String(err)));

      await page.goto('/about');
      await expect(page.locator('[data-go-page-about]')).toBeVisible();
      await markBootCount(page);
      const envelopes = trackEnvelopeRequests(page);

      const sel = await injectAnchor(page, 'h09-modified', { href: '/docs' });
      // Earlier cases can open and close browser windows. Activate this page
      // before testing a native middle click in the shared browser process.
      await page.bringToFront();
      const [popup] = await Promise.all([
        context.waitForEvent('page'),
        page.click(sel, tc.opts),
      ]);
      // The browser (not the interceptor) handled it: a new context
      // opened carrying the destination...
      await popup.waitForLoadState('domcontentloaded');
      expect(popup.url()).toContain('/docs');
      // ...and the original tab never moved nor reloaded nor fetched an
      // envelope — the click was not hijacked into a soft navigation.
      expect(page.url()).toContain('/about');
      expect(await bootCount(page), 'source tab must be unchanged').toBe(1);
      await expect(page.locator('[data-go-page-about]')).toBeVisible();
      expect(envelopes.count(), 'no envelope fetch for the original tab').toBe(0);
      await popup.close();

      expect(errors, 'no browser JavaScript errors').toEqual([]);
    });
  }

  test('target="_blank" opens a new tab, original unchanged', async ({ page, context }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/about');
    await markBootCount(page);
    const envelopes = trackEnvelopeRequests(page);

    const sel = await injectAnchor(page, 'h09-blank', {
      href: '/docs',
      target: '_blank',
    });
    const [popup] = await Promise.all([
      context.waitForEvent('page'),
      page.click(sel),
    ]);
    await popup.waitForLoadState('domcontentloaded');
    expect(popup.url()).toContain('/docs');

    expect(page.url()).toContain('/about');
    expect(await bootCount(page), 'source tab must be unchanged').toBe(1);
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    expect(envelopes.count(), 'no envelope fetch for the original tab').toBe(0);
    await popup.close();

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });

  test('download attribute preserves browser download behavior', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/about');
    await markBootCount(page);
    const envelopes = trackEnvelopeRequests(page);

    const sel = await injectAnchor(page, 'h09-download', {
      href: '/docs',
      download: 'docs.html',
    });
    const [download] = await Promise.all([
      page.waitForEvent('download'),
      page.click(sel),
    ]);
    expect(download.suggestedFilename()).toBe('docs.html');
    await download.cancel();

    // The browser ran its own download flow; the SPA never took over.
    expect(page.url()).toContain('/about');
    expect(await bootCount(page), 'source tab must be unchanged').toBe(1);
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    expect(envelopes.count(), 'no envelope fetch for the original tab').toBe(0);

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });

  test('external-origin link stays native (controlled origin, no network)', async ({ page, context }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/about');
    await markBootCount(page);
    const envelopes = trackEnvelopeRequests(page);

    // A controlled fake origin: the browser's native navigation attempt
    // is observed at the network layer (context-level route) and served
    // a minimal document — no real network access, no error page. Landing
    // on the foreign origin proves the browser (not the SPA) handled the
    // click; zero envelope fetches prove the interceptor never claimed it.
    let externalAttempt: string | null = null;
    await context.route('http://external-h09.test/**', (route) => {
      externalAttempt = route.request().url();
      return route.fulfill({
        status: 200,
        contentType: 'text/html',
        body: '<!doctype html><title>external</title>external ok',
      });
    });
    const sel = await injectAnchor(page, 'h09-external', {
      href: 'http://external-h09.test/elsewhere',
    });
    await page.click(sel);
    await expect
      .poll(() => externalAttempt, { message: 'browser must attempt the native navigation' })
      .toContain('external-h09.test');
    await page.waitForURL('**external-h09.test/elsewhere');
    await expect(page.locator('body')).toHaveText('external ok');
    expect(envelopes.count(), 'no envelope fetch for the original tab').toBe(0);

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });

  test('fragment-only link scrolls natively without a navigation envelope', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/docs');
    await expect(page.locator('[data-go-page-docs]')).toBeVisible();
    await markBootCount(page);
    const envelopes = trackEnvelopeRequests(page);

    // An in-document target and a plain fragment link.
    await page.evaluate(() => {
      const h2 = document.createElement('h2');
      h2.id = 'h09-section';
      h2.textContent = 'H09 anchor target';
      document.querySelector('[data-go-page-outlet]')!.appendChild(h2);
      const a = document.createElement('a');
      a.id = 'h09-hash';
      a.textContent = 'hash link';
      a.href = '#h09-section';
      document.body.appendChild(a);
    });

    await page.click('#h09-hash');

    // Native anchor semantics: the fragment landed in the address bar and
    // the element became the scroll target...
    await expect
      .poll(() => page.evaluate(() => window.location.hash))
      .toBe('#h09-section');
    // ...two raf ticks later still no envelope fetch and no reload: the
    // interceptor never claimed this click.
    await page.evaluate(
      () => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())))
    );
    expect(envelopes.count(), 'fragment-only must not fetch an envelope').toBe(0);
    expect(await bootCount(page), 'hash navigation must not reload').toBe(1);

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });

  test('data-no-spa and rel=external opt out to full-document navigation', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/about');
    await markBootCount(page);

    // Explicit opt-out: the browser performs a REAL navigation — marker
    // wiped, fresh document. A soft navigation would have kept it.
    const selNoSpa = await injectAnchor(page, 'h09-nospa', {
      href: '/dashboard',
      'data-no-spa': '',
    });
    await page.click(selNoSpa);
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    expect(page.url()).toContain('/dashboard');
    expect(
      await bootCount(page),
      'data-no-spa must be a full-document navigation'
    ).toBeUndefined();

    await page.goto('/about');
    await markBootCount(page);
    const selRel = await injectAnchor(page, 'h09-relexternal', {
      href: '/dashboard',
      rel: 'external',
    });
    await page.click(selRel);
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    expect(page.url()).toContain('/dashboard');
    expect(
      await bootCount(page),
      'rel=external must be a full-document navigation'
    ).toBeUndefined();

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });
});
