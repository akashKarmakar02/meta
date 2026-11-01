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

test.describe('G06: remount-free return navigation /dashboard/settings → /dashboard', () => {
  test('returning to /dashboard keeps the layout instance, its state, and mountCount at 1', async ({ page }) => {
    // The G06 return leg boots AT SETTINGS (direct-load shell), the path
    // G05's forward tests never exercised.
    await page.goto('/dashboard/settings');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');

    // One layout, one page root after boot; tag identities for re-finding.
    expect(await page.locator('[data-go-layout]').count()).toBe(1);
    expect(await page.locator('[data-go-page-root]').count()).toBe(1);
    await page.evaluate(() => {
      document.querySelector('[data-go-layout]')!.setAttribute('data-e2e-id', 'marker');
      document.querySelector('[data-go-layout-shell-host]')!.setAttribute('data-e2e-id', 'host');
      document.querySelector('[data-go-layout-outlet]')!.setAttribute('data-e2e-id', 'outlet');
      document.querySelector('[data-go-dashboard-sidebar]')!.setAttribute('data-e2e-id', 'sidebar');
      (window as unknown as Record<string, unknown>).__e2eSoftNav = true;
    });

    // State accumulated in Settings must survive the return.
    await page.click('[data-go-layout-counter-button]');
    await page.click('[data-go-layout-counter-button]');
    await page.click('[data-go-layout-counter-button]');
    await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 3');

    // The return navigation under test: a trusted click on "Overview".
    await page.click('a[href="/dashboard"]');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');

    // Soft navigation — same document, no reload.
    expect(page.url()).toContain('/dashboard');
    const softNav = await page.evaluate(
      () => (window as unknown as Record<string, unknown>).__e2eSoftNav === true
    );
    expect(softNav, 'return navigation was soft, no document reload').toBe(true);

    // Same layout elements by identity.
    expect(await page.locator('[data-go-layout][data-e2e-id="marker"]').count()).toBe(1);
    expect(await page.locator('[data-go-layout-shell-host][data-e2e-id="host"]').count()).toBe(1);
    expect(await page.locator('[data-go-layout-outlet][data-e2e-id="outlet"]').count()).toBe(1);
    expect(await page.locator('[data-go-dashboard-sidebar][data-e2e-id="sidebar"]').count()).toBe(1);
    // Exactly one active Svelte layout.
    expect(await page.locator('[data-go-layout]').count()).toBe(1);

    // Counter preserved and still reactive after the return.
    await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 3');
    await page.click('[data-go-layout-counter-button]');
    await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 4');

    // Exactly one page root, inside the retained outlet, destination metadata.
    expect(await page.locator('[data-go-page-root]').count()).toBe(1);
    const root = page.locator('[data-go-page-root]');
    await expect(root).toHaveAttribute('data-go-route', '/dashboard');
    await expect(root).toHaveAttribute('data-go-route-id', 'dashboard/page.svelte');
    const inside = await page.evaluate(() =>
      document.querySelector('[data-go-page-root]')!.closest('[data-go-layout-outlet]') !== null
    );
    expect(inside, 'returned page lives inside the retained outlet').toBe(true);
    // The Settings page is gone.
    expect(await page.locator('[data-go-page="dashboard/settings"]').count()).toBe(0);
    await expect(page).toHaveTitle('Dashboard');
  });

  test('repeated return cycles never leak page instances or remount the layout', async ({ page }) => {
    await page.goto('/dashboard/settings');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');
    await page.evaluate(() => {
      document.querySelector('[data-go-layout]')!.setAttribute('data-e2e-id', 'marker');
      (window as unknown as Record<string, unknown>).__e2eSoftNav = true;
    });

    // Alternating return legs: each direction is a child swap, none may
    // remount the layout or stack a second page.
    for (let i = 0; i < 3; i++) {
      await page.click('a[href="/dashboard"]');
      await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
      expect(await page.locator('[data-go-page-root]').count()).toBe(1);
      expect(await page.locator('[data-go-layout]').count()).toBe(1);
      await page.click('a[href="/dashboard/settings"]');
      await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');
      expect(await page.locator('[data-go-page-root]').count()).toBe(1);
      expect(await page.locator('[data-go-layout]').count()).toBe(1);
    }

    // One layout marker instance across all six swaps, layout reactive.
    expect(await page.locator('[data-go-layout][data-e2e-id="marker"]').count()).toBe(1);
    await page.click('[data-go-layout-counter-button]');
    await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 1');
  });

  test('browser Back from the returned /dashboard restores Settings without remount', async ({ page }) => {
    // History integrity of the return leg: settings → dashboard (push) →
    // Back must restore Settings into the SAME retained layout.
    await page.goto('/dashboard/settings');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');
    await page.evaluate(() => {
      document.querySelector('[data-go-layout]')!.setAttribute('data-e2e-id', 'marker');
      (window as unknown as Record<string, unknown>).__e2eSoftNav = true;
    });

    await page.click('a[href="/dashboard"]');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');

    await page.goBack();
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');
    expect(page.url()).toContain('/dashboard/settings');
    const softNav = await page.evaluate(
      () => (window as unknown as Record<string, unknown>).__e2eSoftNav === true
    );
    expect(softNav, 'Back restored via popstate, no document reload').toBe(true);
    expect(await page.locator('[data-go-layout][data-e2e-id="marker"]').count()).toBe(1);
    expect(await page.locator('[data-go-page-root]').count()).toBe(1);
    await expect(page.locator('[data-go-page-root]')).toHaveAttribute(
      'data-go-route-id',
      'dashboard/settings/page.svelte'
    );

    // Forward again — the history entry the return leg created works too.
    await page.goForward();
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    expect(await page.locator('[data-go-layout][data-e2e-id="marker"]').count()).toBe(1);
    expect(await page.locator('[data-go-page-root]').count()).toBe(1);
  });
});
