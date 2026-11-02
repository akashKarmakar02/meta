// H08 acceptance (blueprint §7): persisted dashboard layout state —
// state remains between children.
//
// Scope guard: the G05/G06 specs already prove one swap and the return
// leg preserve the layout; this dedicated H08 acceptance suite pins the
// full sibling-navigation contract in one place:
//
//   - the SAME layout component instance (host, marker, sidebar, outlet
//     tagged by identity attributes) survives every child navigation;
//   - layout-local counter state persists across swaps in BOTH
//     directions and over repeated alternation, starting from either
//     child route;
//   - the layout stays interactive after each swap;
//   - only the child page changes: route metadata flips, the outgoing
//     page's DOM is gone, exactly one page root exists;
//   - the whole run happens without a document reload (__BOOT_COUNT).
//
// Trusted Playwright clicks only. Subtree exit/re-entry state reset is
// G08's acceptance, not H08's — "state remains between children".
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

test.describe('H08: persisted dashboard layout state between children', () => {
  test('same layout instance, state and reactivity survive /dashboard → /dashboard/settings', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/dashboard');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    await markBootCount(page);

    // Tag every layout-owned element so identity can be re-proven after
    // the swap — element identity, not just presence.
    await page.evaluate(() => {
      document.querySelector('[data-go-layout]')!.setAttribute('data-e2e-id', 'marker');
      document.querySelector('[data-go-layout-shell-host]')!.setAttribute('data-e2e-id', 'host');
      document.querySelector('[data-go-dashboard-sidebar]')!.setAttribute('data-e2e-id', 'sidebar');
      document.querySelector('[data-go-layout-outlet]')!.setAttribute('data-e2e-id', 'outlet');
    });

    // Nonzero layout-local state before navigating.
    await page.click('[data-go-layout-counter-button]');
    await page.click('[data-go-layout-counter-button]');
    await page.click('[data-go-layout-counter-button]');
    await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 3');

    // The sibling navigation under test.
    await page.click('a[href="/dashboard/settings"]');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');

    // Same document, same layout elements by tagged identity.
    expect(await bootCount(page), 'child swap must not reload').toBe(1);
    expect(await page.locator('[data-go-layout][data-e2e-id="marker"]').count()).toBe(1);
    expect(await page.locator('[data-go-layout-shell-host][data-e2e-id="host"]').count()).toBe(1);
    expect(await page.locator('[data-go-dashboard-sidebar][data-e2e-id="sidebar"]').count()).toBe(1);
    expect(await page.locator('[data-go-layout-outlet][data-e2e-id="outlet"]').count()).toBe(1);
    // No second anything: exactly one layout host, one page root.
    expect(await page.locator('[data-go-layout-shell-host]').count()).toBe(1);
    expect(await page.locator('[data-go-page-root]').count()).toBe(1);

    // State survived the swap...
    await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 3');
    // ...and the SAME instance is still reactive, not a frozen copy.
    await page.click('[data-go-layout-counter-button]');
    await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 4');

    // Only the child changed: the new page root carries the settings
    // metadata, sits inside the retained outlet, and the old page is gone.
    const root = page.locator('[data-go-page-root]');
    await expect(root).toHaveAttribute('data-go-route', '/dashboard/settings');
    await expect(root).toHaveAttribute('data-go-route-id', 'dashboard/settings/page.svelte');
    const inside = await page.evaluate(() =>
      document.querySelector('[data-go-page-root]')!.closest('[data-go-layout-outlet]') !== null
    );
    expect(inside, 'page root lives inside the retained outlet').toBe(true);
    expect(await page.locator('main[data-go-page="dashboard"]').count()).toBe(0);
    await expect(page).toHaveTitle('Settings');

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });

  test('direct-loaded settings shell keeps state across the return swap too', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    // The opposite direction, booting AT the deep child: the bootstrap's
    // adopted layout must survive its first soft navigation exactly like
    // a pipeline-mounted one.
    await page.goto('/dashboard/settings');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');
    await markBootCount(page);
    await page.evaluate(() => {
      document.querySelector('[data-go-layout]')!.setAttribute('data-e2e-id', 'marker');
    });

    await page.click('[data-go-layout-counter-button]');
    await page.click('[data-go-layout-counter-button]');
    await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 2');

    await page.click('a[href="/dashboard"]');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    await expect(page).toHaveTitle('Dashboard');

    expect(await bootCount(page), 'return swap must not reload').toBe(1);
    expect(await page.locator('[data-go-layout][data-e2e-id="marker"]').count()).toBe(1);
    await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 2');
    expect(await page.locator('[data-go-page-root]').count()).toBe(1);
    // The settings page DOM is gone.
    expect(await page.locator('main[data-go-page="dashboard/settings"]').count()).toBe(0);

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });

  test('repeated alternation never resets or duplicates state, in either direction', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (err) => errors.push(String(err)));

    await page.goto('/dashboard');
    await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
    await markBootCount(page);
    await page.evaluate(() => {
      document.querySelector('[data-go-layout]')!.setAttribute('data-e2e-id', 'marker');
    });

    // Five alternating swaps with a growing value: the count grows
    // monotonically across every hop — a remount would flatten it to 0.
    for (let i = 1; i <= 5; i++) {
      // One increment per cycle, before the hop under test.
      await page.click('[data-go-layout-counter-button]');
      await page.click('a[href="/dashboard/settings"]');
      await expect(page.locator('[data-go-page-root] h1')).toHaveText('Settings');
      await expect(page.locator('[data-go-layout-counter]')).toHaveText(`Layout count: ${i}`);
      expect(await page.locator('[data-go-page-root]').count()).toBe(1);
      expect(await page.locator('[data-go-layout-shell-host]').count()).toBe(1);

      await page.click('a[href="/dashboard"]');
      await expect(page.locator('[data-go-page-root] h1')).toHaveText('Dashboard');
      await expect(page.locator('[data-go-layout-counter]')).toHaveText(`Layout count: ${i}`);
      expect(await page.locator('[data-go-page-root]').count()).toBe(1);
      expect(await page.locator('[data-go-layout-shell-host]').count()).toBe(1);
    }

    expect(await bootCount(page), 'no swap in the run reloaded the document').toBe(1);
    expect(
      await page.locator('[data-go-layout][data-e2e-id="marker"]').count()
    ).toBe(1);
    await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 5');

    // After all that churn the layout is still interactive.
    await page.click('[data-go-layout-counter-button]');
    await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 6');

    expect(errors, 'no browser JavaScript errors').toEqual([]);
  });
});
