import { expect, test } from '@playwright/test';

test('clicking the current dashboard link preserves the document and state', async ({ page }) => {
  await page.goto('/dashboard');
  await expect(page.locator('[data-go-dashboard-sidebar]')).toBeVisible();
  await page.locator('[data-go-layout-counter-button]').click();
  await page.getByRole('button', { name: 'Increment', exact: true }).click();
  await page.evaluate(() => {
    (window as unknown as Record<string, unknown>).__samePageMarker = true;
    document.querySelector('[data-go-layout-shell]')?.setAttribute('data-test-layout', 'original');
    document.querySelector('[data-go-page="dashboard"]')?.setAttribute('data-test-page', 'original');
  });
  let requests = 0;
  page.on('request', (request) => {
    if (request.headers()['x-go-web-navigation']) requests++;
  });
  await page.locator('[data-go-dashboard-side-nav] a[href="/dashboard"]').click();
  expect(await page.evaluate(() => (window as unknown as Record<string, unknown>).__samePageMarker)).toBe(true);
  await expect(page.locator('[data-go-layout-shell][data-test-layout="original"]')).toHaveCount(1);
  await expect(page.locator('[data-go-page="dashboard"][data-test-page="original"]')).toHaveCount(1);
  await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 1');
  await expect(page.locator('[data-go-page="dashboard"] p')).toHaveText('Count: 1');
  expect(requests).toBe(0);
});

test('direct dashboard routes render only their nearest visual layout', async ({ page }) => {
  for (const path of ['/dashboard', '/dashboard/settings']) {
    await page.goto(path);
    await expect(page.locator('[data-go-dashboard-sidebar]')).toBeVisible();
    await expect(page.locator('[data-go-layout="app/dashboard/layout.svelte"]')).toHaveCount(1);
    await expect(page.locator('[data-go-layout="app/layout.templ"]')).toHaveCount(0);
    await expect(page.locator('[data-go-site-nav]')).toHaveCount(0);
    await expect(page.locator('[data-go-page-root]')).toHaveCount(1);
    await expect(page.locator('html')).toHaveCount(1);
    await page.reload();
    await expect(page.locator('[data-go-dashboard-sidebar]')).toBeVisible();
    await expect(page.locator('[data-go-site-nav]')).toHaveCount(0);
  }
});

test('switches visual layouts and preserves the shared dashboard layout', async ({ page }) => {
  await page.goto('/about');
  await expect(page.locator('[data-go-layout="app/layout.templ"]')).toHaveCount(1);
  await page.locator('[data-go-site-nav] a[href="/dashboard"]').click();
  await expect(page.locator('[data-go-dashboard-sidebar]')).toBeVisible();
  await expect(page.locator('[data-go-site-nav]')).toHaveCount(0);
  await page.locator('[data-go-layout-counter-button]').click();
  await page.locator('[data-go-dashboard-side-nav] a[href="/dashboard/settings"]').click();
  await expect(page.locator('[data-go-layout-counter]')).toHaveText('Layout count: 1');
  await page.locator('[data-go-dashboard-side-nav] a[href="/about"]').click();
  await expect(page.locator('[data-go-dashboard-sidebar]')).toHaveCount(0);
  await expect(page.locator('[data-go-site-nav]')).toHaveCount(1);
  await page.goBack();
  await expect(page.locator('[data-go-dashboard-sidebar]')).toBeVisible();
  await expect(page.locator('[data-go-site-nav]')).toHaveCount(0);
  await page.goForward();
  await expect(page.locator('[data-go-site-nav]')).toHaveCount(1);
});
