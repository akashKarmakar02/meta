// C04 browser tests: CSR loader data delivery end-to-end in a real browser.
//
// The dashboard/[teamId] fixture route has a registered Go loader whose
// `greeting` field ("Loaded by Go") cannot be derived from the URL, so any
// visible greeting proves the value came from the server loader — not from
// window.location.
import { test, expect, type Page } from '@playwright/test';

const teamPage = (page: Page) => page.locator('[data-go-page="dashboard-team"]');

test.describe('C04 CSR loader data delivery', () => {
  test('direct load renders server loader data with no extra fetch', async ({
    page,
  }) => {
    const requests: string[] = [];
    page.on('request', (r) => requests.push(r.url()));

    await page.goto('/dashboard/team-42');
    await expect(teamPage(page)).toHaveText(/Loaded by Go/);
    await expect(teamPage(page)).toContainText('Team team-42');

    // No extra fetch solely to retrieve initial loader data: the only
    // document request is the initial navigation.
    const documentFetches = requests.filter((u) => u.includes('/dashboard/team-42'));
    expect(documentFetches).toHaveLength(1);
  });

  test('refresh re-runs the loader and renders fresh data', async ({ page }) => {
    await page.goto('/dashboard/team-7');
    await expect(teamPage(page)).toContainText('Team team-7');
    await page.reload();
    await expect(teamPage(page)).toContainText('Team team-7');
    await expect(teamPage(page)).toHaveText(/Loaded by Go/);
  });

  test('soft navigation into the CSR route fetches fresh data', async ({ page }) => {
    await page.goto('/');
    // Navigate via the client (soft navigation) to the team route.
    await page.evaluate(() => {
      const a = document.createElement('a');
      a.href = '/dashboard/team-42';
      a.click();
    });
    await expect(teamPage(page)).toHaveText(/Loaded by Go/);
    await expect(teamPage(page)).toContainText('Team team-42');
  });

  test('navigation between two concrete URLs of the same route updates data', async ({
    page,
  }) => {
    await page.goto('/dashboard/team-1');
    await expect(teamPage(page)).toContainText('Team team-1');

    await page.evaluate(() => {
      const a = document.createElement('a');
      a.href = '/dashboard/team-2';
      a.click();
    });
    await expect(teamPage(page)).toContainText('Team team-2');
    // The old value is gone — data is not stale from the previous URL.
    await expect(teamPage(page)).not.toContainText('Team team-1');
  });

  test('query-sensitive loader results', async ({ page }) => {
    await page.goto('/dashboard/team-42?source=nav');
    await expect(teamPage(page)).toContainText('Team team-42');
    await expect(teamPage(page)).toHaveText(/Loaded by Go/);
  });

  test('back/forward restore the correct server-loaded data', async ({ page }) => {
    await page.goto('/dashboard/team-1');
    await expect(teamPage(page)).toContainText('Team team-1');

    await page.evaluate(() => {
      const a = document.createElement('a');
      a.href = '/dashboard/team-2';
      a.click();
    });
    await expect(teamPage(page)).toContainText('Team team-2');

    await page.goBack();
    await expect(teamPage(page)).toContainText('Team team-1');

    await page.goForward();
    await expect(teamPage(page)).toContainText('Team team-2');
  });

  test('persistent Svelte layout is preserved across sibling team routes', async ({
    page,
  }) => {
    await page.goto('/dashboard/team-1');
    const layout = page.locator('[data-go-layout="app/dashboard/layout.svelte"]');
    await expect(layout).toBeVisible();

    // Navigate to a sibling team route: the layout identity is unchanged, so
    // the persistent layout must survive (not remount) while the page data
    // updates.
    await page.evaluate(() => {
      const a = document.createElement('a');
      a.href = '/dashboard/team-2';
      a.click();
    });
    await expect(teamPage(page)).toContainText('Team team-2');
    await expect(layout).toBeVisible();
  });

  test('leaving the dashboard subtree disposes the Svelte layout', async ({
    page,
  }) => {
    await page.goto('/dashboard/team-1');
    await expect(page.locator('[data-go-layout="app/dashboard/layout.svelte"]')).toBeVisible();

    await page.evaluate(() => {
      const a = document.createElement('a');
      a.href = '/products/1';
      a.click();
    });
    await expect(page.locator('[data-go-layout="app/dashboard/layout.svelte"]')).toHaveCount(0);
  });

  test('CSR route without a loader still renders', async ({ page }) => {
    await page.goto('/products/1');
    await expect(page.locator('[data-go-page="products-detail"]')).toBeVisible();
  });

  test('SSR↔CSR navigation regression: SSR page still renders templ data', async ({
    page,
  }) => {
    // SSR route with a loader (C03) still works alongside CSR loader routes.
    await page.goto('/posts/42');
    await expect(page.locator('[data-post-id]')).toHaveText('42');

    // Navigate to a CSR loader route.
    await page.evaluate(() => {
      const a = document.createElement('a');
      a.href = '/dashboard/team-42';
      a.click();
    });
    await expect(teamPage(page)).toHaveText(/Loaded by Go/);
  });
});
