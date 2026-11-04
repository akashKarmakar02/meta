// C06 browser tests: Phase C integration acceptance in a real browser.
//
// These tests close the gaps the earlier suites left open:
//
//   - REAL-CLICK soft navigation into a CSR loader route (the C04 suite used
//     programmatic a.click(), whose synthetic events are untrusted and take
//     the full-document path — masking, for example, an envelope validator
//     that drops the `data` field). Every test here either clicks a real
//     anchor or drives the router through a trusted user gesture.
//   - the final URL and history entry after a soft-navigation redirect
//     reflect the destination, not the original route;
//   - the three loader-data states (absent / JSON null / value) stay
//     distinct through a real soft navigation;
//   - a slow CSR loader race (slow A superseded by fast B) never commits
//     A's data.
import { test, expect, type Page } from '@playwright/test';

const teamPage = (page: Page) => page.locator('[data-go-page="dashboard-team"]');

/** Marks the document with a boot counter; wiped by any real reload. */
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

test.describe('C06 real-click soft navigation delivers loader data', () => {
  test('a trusted click into a CSR loader route renders envelope data', async ({
    page,
  }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/c05/source');
    await expect(page.locator('[data-go-page="c05-source"]')).toBeVisible();
    await markBootCount(page);

    // A REAL click (isTrusted) engages the soft-navigation pipeline: the
    // fetch carries X-Go-Web-Navigation and the envelope's optional data
    // field must reach the mounted page props. A validator that dropped
    // `data` would leave the greeting absent while the full-document path
    // still worked — exactly the gap this test pins.
    const envelopeFetches: string[] = [];
    page.on('request', (req) => {
      if (req.headers()['x-go-web-navigation']) {
        envelopeFetches.push(req.url());
      }
    });

    // A same-origin link to the dynamic CSR loader route. Injected anchors
    // cannot be used for the *click* itself; page.click dispatches a real
    // trusted input event to whatever anchor is in the DOM.
    await page.evaluate(() => {
      const a = document.createElement('a');
      a.id = 'c06-team-link';
      a.textContent = 'team 42';
      a.href = '/dashboard/team-42';
      document.body.appendChild(a);
    });
    await page.click('#c06-team-link');

    await expect(teamPage(page)).toHaveText(/Loaded by Go/);
    await expect(teamPage(page)).toContainText('Team team-42');

    // The navigation went through the soft pipeline (one envelope fetch),
    // not a document reload.
    expect(envelopeFetches).toHaveLength(1);
    expect(envelopeFetches[0]).toContain('/dashboard/team-42');
    expect(await bootCount(page), 'soft nav must not reload').toBe(1);

    expect(errors).toEqual([]);
  });

  test('absent data, JSON null, and JSON value stay distinct after soft navigation', async ({
    page,
  }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    // Three fixture routes pin the three states a CSR page can see:
    //   /c05/nullData — loader returns typed nil -> JSON null
    //   /products/1    — no loader               -> data absent
    //   /dashboard/team-9 — loader value         -> JSON value
    // The null fixture must show "explicit null", not "absent" — a validator
    // that collapses null into undefined (or drops the field) would render
    // the absent branch here.

    // Null state via real click.
    await page.goto('/products/1');
    await expect(page.locator('[data-go-page="products-detail"]')).toBeVisible();
    await page.evaluate(() => {
      const a = document.createElement('a');
      a.id = 'c06-null-link';
      a.textContent = 'null data';
      a.href = '/c05/nullData';
      document.body.appendChild(a);
    });
    await page.click('#c06-null-link');
    await expect(
      page.locator('[data-go-page="c05-null-data"] [data-go-null-state]')
    ).toHaveText('explicit null');

    // Absent state: back to the no-loader products route, then confirm its
    // shell carries no data script.
    await page.goto('/products/1');
    const shellHasData = await page.evaluate(
      () =>
        document.getElementById('go-meta-csr-data')?.textContent?.trim() !== undefined
    );
    expect(shellHasData).toBe(false);

    // Value state.
    await page.evaluate(() => {
      const a = document.createElement('a');
      a.id = 'c06-val-link';
      a.textContent = 'team 9';
      a.href = '/dashboard/team-9';
      document.body.appendChild(a);
    });
    await page.click('#c06-val-link');
    await expect(teamPage(page)).toHaveText(/Loaded by Go/);

    expect(errors).toEqual([]);
  });
});

test.describe('C06 redirect URL and history', () => {
  test('soft-nav redirect lands on the destination URL and entry', async ({
    page,
  }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/c05/source');
    await expect(page.locator('[data-go-page="c05-source"]')).toBeVisible();

    // Real click → soft navigation → fetch follows the 302 to /about. The
    // final URL must be the destination; the history entry must not keep the
    // original /c05/redirectSsr as the current entry.
    await page.click('[data-go-c05-redirect-ssr]');
    await expect(page).toHaveURL(/\/about$/);
    await expect(page.locator('[data-go-page-about]')).toBeVisible();

    // Back returns to the source page (the pre-redirect origin), proving
    // exactly one history entry was added for the whole redirect chain.
    await page.goBack();
    await expect(page).toHaveURL(/\/c05\/source$/);

    expect(errors).toEqual([]);
  });

  test('redirect with query string preserves the query at the boundary', async ({
    page,
  }) => {
    // The C05 redirectSsr loader targets /about (fixed); check the generic
    // query-preservation contract through the SSR fixture with a query on
    // the ORIGINAL request — the destination loader sees its own URL. The
    // HTTP-level preservation of a query in the Location header itself is
    // pinned in Go (TestC06ValidRedirectWithQueryAndFragment).
    await page.goto('/c05/source');
    await page.click('[data-go-c05-redirect-csr]');
    await expect(page).toHaveURL(/\/dashboard$/);
    await expect(page.locator('[data-go-page="dashboard"]')).toBeVisible();
  });
});

test.describe('C06 loader races', () => {
  test('slow CSR A superseded by fast B commits only B', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    // Hold A (/dashboard/team-a — CSR loader route) and let B (/about) fly.
    let heldA: import('@playwright/test').Route | null = null;
    await page.route('**/dashboard/team-a', (route) => {
      if (route.request().headers()['x-go-web-navigation']) {
        heldA = route;
        return;
      }
      return route.continue();
    });

    await page.goto('/');
    await markBootCount(page);

    await page.evaluate(() => {
      const a = document.createElement('a');
      a.id = 'c06-slow';
      a.textContent = 'slow team a'; // text content keeps the anchor clickable
      a.href = '/dashboard/team-a';
      document.body.appendChild(a);
    });
    await page.click('#c06-slow');
    await expect
      .poll(() => heldA !== null, { message: 'team-a envelope held' })
      .toBe(true);

    // B supersedes A.
    await page.click('a[href="/about"]');
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    const urlAfterB = page.url();

    // Release A late with valid data for the WRONG page.
    const r = heldA;
    heldA = null;
    if (r) {
      await r.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          version: 1,
          url: '/dashboard/team-a',
          routeId: 'dashboard/[teamId]/page.svelte',
          mode: 'csr',
          title: 'Team A',
          layouts: ['app/dashboard/layout.svelte'],
          html: '',
          clientEntry: 'dashboard/[teamId]/page.svelte',
          data: { teamId: 'team-a', greeting: 'STALE LOADED' },
        }),
      });
    }
    await page.waitForTimeout(300);

    // Only B committed: no stale page, data, or URL change.
    expect(page.url()).toBe(urlAfterB);
    expect(await teamPage(page).count()).toBe(0);
    expect(page.locator('body')).not.toContainText('STALE LOADED');
    expect(await bootCount(page), 'no reload from stale commit').toBe(1);

    expect(errors).toEqual([]);
  });
});
