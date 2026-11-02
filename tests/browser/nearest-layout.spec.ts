import { expect, test } from '@playwright/test';

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
