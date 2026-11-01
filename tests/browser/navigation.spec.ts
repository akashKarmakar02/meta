// Real-browser navigation tests (blueprint §7): the checks only a real
// Chromium can make — DOM identity of retained layouts, persistent
// layout state across child-only swaps, and no-reload soft navigation —
// against the production Go binary + Vite bundle.
//
// Trusted clicks are the default here (page.click dispatches real input
// events), so these tests exercise exactly what a user's click does,
// including the router's trust boundary (F05). Happy-dom cannot make
// these claims: it has no layout, no event trust, no real navigation.
import { test, expect } from '@playwright/test';

test.describe('G05: child-only swap inside a retained persistent layout', () => {
  test('/dashboard → /dashboard/settings preserves the layout instance and its state', async ({ page }) => {
    await page.goto('/dashboard');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');

    // One layout, one page root after boot.
    expect(await page.locator('[data-go-layout]').count()).toBe(1);
    expect(await page.locator('[data-go-page-root]').count()).toBe(1);

    // Increment the sidebar counter twice; the value must survive the swap.
    await page.click('[data-go-layout-counter-button]');
    await page.click('[data-go-layout-counter-button]');
    await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 2');

    // Tag every layout-owned element with an identity attribute we can
    // re-find after the swap — element identity is the G05 claim.
    await page.evaluate(() => {
      document.querySelector('[data-go-layout]')!.setAttribute('data-e2e-id', 'marker');
      document.querySelector('[data-go-layout-shell-host]')!.setAttribute('data-e2e-id', 'host');
      document.querySelector('[data-go-layout-outlet]')!.setAttribute('data-e2e-id', 'outlet');
      document.querySelector('[data-go-dashboard-sidebar]')!.setAttribute('data-e2e-id', 'sidebar');
      // A full document load wipes this flag; a soft navigation keeps it.
      (window as unknown as Record<string, unknown>).__e2eSoftNav = true;
    });

    // The soft navigation under test: a real trusted click on the link.
    await page.click('a[href="/dashboard/settings"]');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');

    // Same document — no reload happened.
    expect(page.url()).toContain('/dashboard/settings');
    const softNav = await page.evaluate(
      () => (window as unknown as Record<string, unknown>).__e2eSoftNav === true
    );
    expect(softNav, 'navigation was soft, no document reload').toBe(true);

    // Layout identity preserved: the SAME elements, found by their tags.
    expect(await page.locator('[data-go-layout][data-e2e-id="marker"]').count()).toBe(1);
    expect(await page.locator('[data-go-layout-shell-host][data-e2e-id="host"]').count()).toBe(1);
    expect(await page.locator('[data-go-layout-outlet][data-e2e-id="outlet"]').count()).toBe(1);
    expect(await page.locator('[data-go-dashboard-sidebar][data-e2e-id="sidebar"]').count()).toBe(1);

    // State preserved: the counter is untouched by the swap.
    await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 2');

    // Exactly one page root, inside the retained outlet, with correct metadata.
    expect(await page.locator('[data-go-page-root]').count()).toBe(1);
    const root = page.locator('[data-go-page-root]');
    await expect(root).toHaveAttribute('data-go-route', '/dashboard/settings');
    await expect(root).toHaveAttribute('data-go-route-id', 'dashboard/settings/page.svelte');
    const inside = await page.evaluate(() =>
      document.querySelector('[data-go-page-root]')!.closest('[data-go-layout-outlet]') !== null
    );
    expect(inside, 'page root lives inside the retained outlet').toBe(true);

    // Title follows the destination (history commit, F10).
    await expect(page).toHaveTitle('Settings');
  });

  test('repeated swaps never duplicate nodes and keep identity across both directions', async ({ page }) => {
    await page.goto('/dashboard');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    await page.evaluate(() => {
      document.querySelector('[data-go-layout]')!.setAttribute('data-e2e-id', 'marker');
    });

    // Forward, back, forward again — three child swaps, one layout.
    await page.click('a[href="/dashboard/settings"]');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');
    await page.click('a[href="/dashboard"]');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    await page.click('a[href="/dashboard/settings"]');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');

    expect(await page.locator('[data-go-page-root]').count()).toBe(1);
    expect(await page.locator('[data-go-layout-shell]').count()).toBe(1);
    expect(await page.locator('[data-go-layout][data-e2e-id="marker"]').count()).toBe(1);

    // The layout stays reactive after repeated swaps.
    await page.click('[data-go-layout-counter-button]');
    await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 1');
  });

  test('popstate back/forward reuses the retained layout without reload', async ({ page }) => {
    await page.goto('/dashboard');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    await page.evaluate(() => {
      document.querySelector('[data-go-layout]')!.setAttribute('data-e2e-id', 'marker');
    });
    await page.click('a[href="/dashboard/settings"]');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');

    // Browser back — popstate restoration path (F10/G05 retention).
    await page.goBack();
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    expect(await page.locator('[data-go-page-root]').count()).toBe(1);
    expect(await page.locator('[data-go-layout][data-e2e-id="marker"]').count()).toBe(1);

    // Forward again — still one instance, Settings restored.
    await page.goForward();
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');
    expect(await page.locator('[data-go-page-root]').count()).toBe(1);
    expect(await page.locator('[data-go-layout][data-e2e-id="marker"]').count()).toBe(1);
  });
});
