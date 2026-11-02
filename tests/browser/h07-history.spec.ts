// H07 acceptance (blueprint §7): back/forward, direct refresh, deep
// link — history correct.
//
// Scope guard: the Phase F/G specs already prove popstate restoration
// INSIDE the dashboard subtree (G05) and Back after a subtree exit (G07).
// This file owns what those do not:
//
//   - multi-hop SSR history traversal (/ → /about → /docs, back and
//     forward through both entries) with document-lifetime proof;
//   - cross-mode history round trips (/about ⇄ /dashboard) staying soft
//     in BOTH directions;
//   - history entry accounting: traversal never appends duplicate
//     entries;
//   - direct refresh of SSR and CSR routes re-rendering the current route;
//   - fresh-context deep links into nested CSR routes, query string
//     included.
//
// All interactions are trusted Playwright input events or browser-native
// Back/Forward (page.goBack/goForward); no synthetic clicks. A
// window.__BOOT_COUNT marker set from the test is the document-lifetime
// proof: wiped by any real navigation, kept by every soft one.
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

/** Asserts the boot counter survived — the last step did not reload. */
async function expectSoft(page: import('@playwright/test').Page, step: string): Promise<void> {
  expect(await bootCount(page), `${step} must not reload the document`).toBe(1);
}

test.describe('H07: browser history, refresh, deep links', () => {
  test('SSR history traversal: / → /about → /docs, Back and Forward stay soft', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/');
    await expect(page.locator('[data-go-page="home"]')).toBeVisible();
    await markBootCount(page);

    // Two soft pushes build the history stack.
    await page.click('nav[data-go-site-nav] a[href="/about"]');
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    await page.click('nav[data-go-site-nav] a[href="/docs"]');
    await expect(page.locator('[data-go-page-docs]')).toBeVisible();
    const pushed = await page.evaluate(() => window.history.length);

    // Back: /docs → /about — popstate restoration, no reload, no new entry.
    await page.goBack();
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    expect(page.url()).toContain('/about');
    await expect(page).toHaveTitle('About');
    await expectSoft(page, 'Back to /about');

    // Back again: /about → / — the first entry restores too.
    await page.goBack();
    await expect(page.locator('[data-go-page="home"]')).toBeVisible();
    expect(page.url()).toContain('/');
    await expectSoft(page, 'Back to /');

    // Forward through both entries: history replays without appending.
    await page.goForward();
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    await expect(page).toHaveTitle('About');
    await expectSoft(page, 'Forward to /about');
    await page.goForward();
    await expect(page.locator('[data-go-page-docs]')).toBeVisible();
    expect(page.url()).toContain('/docs');
    await expect(page).toHaveTitle('Docs');
    await expectSoft(page, 'Forward to /docs');

    // Traversal appended nothing: same stack depth after four hops.
    expect(await page.evaluate(() => window.history.length)).toBe(pushed);

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });

  test('cross-mode history: /about ⇄ /dashboard round trips stay soft both ways', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    // Start on an SSR document so the persistent server layout and its
    // page outlet exist — the structure soft traversal requires.
    await page.goto('/about');
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    await markBootCount(page);

    // Soft cross-mode entry: the dashboard subtree mounts client-side.
    await page.click('nav[data-go-site-nav] a[href="/dashboard"]');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    await expect(page).toHaveTitle('Dashboard');
    await expectSoft(page, 'soft entry to /dashboard');

    // Back out of the subtree: graceful exit + SSR restoration, all soft.
    await page.goBack();
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    expect(page.url()).toContain('/about');
    await expect(page).toHaveTitle('About');
    await expectSoft(page, 'Back to /about');
    await expect(page.locator('[data-go-layout-shell-host]')).toHaveCount(0);
    await expect(page.locator('[data-go-page-root]')).toHaveCount(0);

    // Forward re-enters the subtree: fresh layout mount, still soft.
    await page.goForward();
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    expect(page.url()).toContain('/dashboard');
    await expect(page).toHaveTitle('Dashboard');
    await expectSoft(page, 'Forward to /dashboard');
    // Exactly one layout instance and one page root — no duplicates from
    // the exit/re-entry cycle.
    expect(await page.locator('[data-go-layout-shell-host]').count()).toBe(1);
    expect(await page.locator('[data-go-page-root]').count()).toBe(1);
    // Fresh instance: the re-mounted layout state starts at zero.
    await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 0');

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });

  test('dashboard ⇄ settings traversal adds no duplicate history entries', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/dashboard');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    await markBootCount(page);

    // One soft push into the sibling child route.
    await page.click('a[href="/dashboard/settings"]');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');
    expect(page.url()).toContain('/dashboard/settings');
    await expect(page).toHaveTitle('Settings');
    const pushed = await page.evaluate(() => window.history.length);

    // Traverse back and forward twice: every hop restores the right page,
    // stays soft, keeps one layout instance, and never appends an entry.
    for (let i = 0; i < 2; i++) {
      await page.goBack();
      await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
      expect(page.url()).toContain('/dashboard');
      await expect(page).toHaveTitle('Dashboard');
      await expectSoft(page, `cycle ${i}: Back to /dashboard`);
      expect(await page.locator('[data-go-layout-shell-host]').count()).toBe(1);

      await page.goForward();
      await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');
      expect(page.url()).toContain('/dashboard/settings');
      await expect(page).toHaveTitle('Settings');
      await expectSoft(page, `cycle ${i}: Forward to /dashboard/settings`);
      expect(await page.locator('[data-go-layout-shell-host]').count()).toBe(1);
    }
    expect(await page.evaluate(() => window.history.length)).toBe(pushed);

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });

  test('direct refresh re-renders the current route', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    // SSR route: reload serves the full document for the current URL.
    await page.goto('/about');
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    await page.reload();
    await expect(page.locator('[data-go-page-about]')).toBeVisible();
    expect(page.url()).toContain('/about');
    // A reload IS a real navigation — the marker must be gone.
    expect(await bootCount(page), 'refresh must be a real navigation').toBeUndefined();

    // CSR route with a persistent layout: reload boots the shell and
    // remounts the subtree fresh.
    await page.goto('/dashboard');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    await page.click('[data-go-layout-counter-button]');
    await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 1');
    await page.reload();
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    expect(page.url()).toContain('/dashboard');
    // Exactly one freshly mounted layout; state reset by the reload.
    expect(await page.locator('[data-go-layout-shell-host]').count()).toBe(1);
    expect(await page.locator('[data-go-page-root]').count()).toBe(1);
    await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 0');

    // Nested CSR route: reload lands on the deep child, not the parent.
    await page.goto('/dashboard/settings');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');
    await page.reload();
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');
    expect(page.url()).toContain('/dashboard/settings');
    const root = page.locator('[data-go-page-root]');
    await expect(root).toHaveAttribute('data-go-route', '/dashboard/settings');
    await expect(root).toHaveAttribute('data-go-route-id', 'dashboard/settings/page.svelte');

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });

  test('fresh-context deep link to a nested CSR route boots and soft-navigates', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    // Deep link straight into the nested route, query string attached:
    // the boot shell carries the metadata; the query travels with the URL.
    await page.goto('/dashboard/settings?tab=profile');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');
    expect(page.url()).toContain('/dashboard/settings?tab=profile');
    const root = page.locator('[data-go-page-root]');
    await expect(root).toHaveAttribute('data-go-route', '/dashboard/settings');
    await expect(root).toHaveAttribute(
      'data-go-route-id',
      'dashboard/settings/page.svelte'
    );
    expect(await page.locator('[data-go-layout-shell-host]').count()).toBe(1);

    // The deep-linked page is interactive, not just rendered.
    await page.getByRole('button', { name: 'Save settings' }).click();
    await expect(page.getByRole('status')).toHaveText('Saved.');

    // Soft navigation out of the deep link and history Back into it both
    // stay soft — the query is preserved through the round trip.
    await markBootCount(page);
    await page.click('a[href="/dashboard"]');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    expect(page.url()).toContain('/dashboard');
    await expectSoft(page, 'soft hop from deep link to /dashboard');
    await page.goBack();
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');
    expect(page.url()).toContain('/dashboard/settings?tab=profile');
    await expectSoft(page, 'Back restores the deep link with its query');

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });
});
