import { test, expect } from '@playwright/test';

test('the reserved image endpoint truthfully reports unavailable processing', async ({ page }) => {
  await page.goto('/about');
  const result = await page.evaluate(async () => {
    const response = await fetch('/_meta/image?src=%2Fimages%2Fhero.jpg&w=960', {
      headers: { Accept: 'image/avif,image/webp,*/*', 'X-Go-Web-Navigation': '1' },
    });
    return { status: response.status, type: response.headers.get('content-type'), body: await response.text() };
  });
  expect(result.status).toBe(501);
  expect(result.type).toContain('text/plain');
  expect(result.body).toContain('not implemented');
  await expect(page.locator('body')).toBeVisible();
});
