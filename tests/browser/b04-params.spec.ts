// B04 acceptance (blueprint §11, §13): typed route parameters in a real browser.
//
// The Go parameter API is server-side; these checks confirm it does not break
// the browser contract: SSR rendering of multiple typed params, soft
// navigation between same-routeId dynamic URLs (no reload), correct routeId
// resolution, and nearest-layout retention.
//
// SSR nested dynamic example: /users/[userId]/posts/[postId]
//   -> <p data-user-id>, <p data-post-id>, <p data-user-id-error>
import { test, expect, type Page } from '@playwright/test';

/** Sets the document-lifetime marker; a full reload wipes it. */
async function markBootCount(page: Page): Promise<void> {
  await page.evaluate(() => {
    (window as unknown as Record<string, unknown>).__BOOT_COUNT = 1;
  });
}

async function bootCount(page: Page): Promise<unknown> {
  return page.evaluate(
    () => (window as unknown as Record<string, unknown>).__BOOT_COUNT
  );
}

test.describe('B04: typed route parameters (SSR)', () => {
  test('1. nested dynamic SSR route renders both typed params', async ({ page }) => {
    await page.goto('/users/42/posts/7');

    await expect(page.locator('[data-user-id]')).toHaveText('42');
    await expect(page.locator('[data-post-id]')).toHaveText('7');
    await expect(page.locator('[data-user-id-error]')).toHaveText('');
    await expect(page.locator('[data-post-id-error]')).toHaveText('');
  });

  test('2. invalid numeric param renders a useful error, not a crash', async ({
    page,
  }) => {
    await page.goto('/users/abc/posts/7');

    await expect(page.locator('[data-user-id]')).toHaveText('');
    await expect(page.locator('[data-post-id]')).toHaveText('7');
    const err = await page.locator('[data-user-id-error]').textContent();
    expect(err).toContain('userId');
    expect(err).toContain('abc');
  });

  test('3. soft navigation between same-routeId dynamic URLs updates both params without reload', async ({
    page,
  }) => {
    await page.goto('/users/42/posts/7');
    await markBootCount(page);
    await expect(page.locator('[data-user-id]')).toHaveText('42');

    // Same routeId, different values: a soft SSR re-render, not a reload.
    // (The navigation envelope wire shape — version 1, no `data` field — is
    // unchanged by B04: params are request-local and never enter the
    // envelope. The F01 fixtures TestNavigationSSRFixture /
    // TestNavigationCSRFixture pin that shape and still pass; here we assert
    // the browser-visible result of the soft navigation.)
    await page.click('a[href="/users/43/posts/8"]');

    expect(await bootCount(page), 'same-routeId nav must not reload').toBe(1);
    await expect(page.locator('[data-user-id]')).toHaveText('43');
    await expect(page.locator('[data-post-id]')).toHaveText('8');
    await expect(page.locator('[data-go-page-users-post]')).toHaveCount(1);
  });

  test('4. nearest layout is retained across the dynamic route', async ({
    page,
  }) => {
    await page.goto('/users/42/posts/7');
    await expect(page.locator('[data-go-layout="app/layout.templ"]')).toHaveCount(1);
    await expect(page.locator('[data-go-page-users-post]')).toHaveCount(1);
  });
});
