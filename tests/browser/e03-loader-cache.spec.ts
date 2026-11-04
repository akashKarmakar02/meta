import { test, expect } from '@playwright/test';

test('SSR cache separates params and queries, survives refresh and expires', async ({ page }) => {
  await page.goto('/cache-demo/post/42?tab=one');
  await expect(page.locator('[data-cache-id]')).toHaveText('42');
  await expect(page.locator('[data-cache-tab]')).toHaveText('one');
  const first = await page.locator('[data-cache-refreshed]').textContent();
  await page.reload();
  await expect(page.locator('[data-cache-refreshed]')).toHaveText(first!);
  await page.goto('/cache-demo/post/99?tab=one');
  await expect(page.locator('[data-cache-id]')).toHaveText('99');
  await page.goto('/cache-demo/post/42?tab=two');
  await expect(page.locator('[data-cache-tab]')).toHaveText('two');
  await page.goto('/cache-demo/post/42?tab=one');
  await expect(page.locator('[data-cache-refreshed]')).toHaveText(first!);
  await page.waitForTimeout(2200);
  await page.reload();
  await expect(page.locator('[data-cache-refreshed]')).not.toHaveText(first!);
});

test('CSR cache serves direct and trusted soft-navigation requests', async ({ page }) => {
  await page.goto('/cache-demo/team/alpha?tab=one');
  await expect(page.locator('[data-cache-id]')).toHaveText('alpha');
  const first = await page.locator('[data-cache-refreshed]').textContent();
  await page.reload();
  await expect(page.locator('[data-cache-refreshed]')).toHaveText(first!);
  await page.evaluate(() => {
    const link = document.createElement('a'); link.href = '/cache-demo/team/beta?tab=two'; link.textContent = 'Open beta'; document.body.append(link);
  });
  await page.getByRole('link', { name: 'Open beta' }).click();
  await expect(page).toHaveURL(/team\/beta\?tab=two/);
  await expect(page.locator('[data-cache-id]')).toHaveText('beta');
  await expect(page.locator('[data-cache-tab]')).toHaveText('two');
  await page.goBack();
  await expect(page.locator('[data-cache-id]')).toHaveText('alpha');
  await expect(page.locator('[data-cache-refreshed]')).toHaveText(first!);
});
