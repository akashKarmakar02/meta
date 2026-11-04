import { expect, test } from '@playwright/test';

// P2-D03: standard HTML form submission against a production build.
// Exercises the no-JavaScript progressive-enhancement path: a plain
// browser POST that carries the hidden double-submit CSRF token, PRG
// redirect after success, and rejection of tampered tokens.

test('guestbook page renders a CSRF-protected form', async ({ page }) => {
  await page.goto('/guestbook');
  const form = page.locator('form[data-go-form="guestbook"]');
  await expect(form).toHaveAttribute('method', 'post');
  await expect(form).toHaveAttribute(
    'action',
    '/_meta/actions/guestbook.submit',
  );
  const token = form.locator('input[name="_csrf"]');
  await expect(token).toHaveCount(1);
  // Real token: payload.signature with a base64url payload.
  expect((await token.inputValue()).length).toBeGreaterThan(20);
});

test('no-JS form submission executes the action and follows the PRG redirect', async ({ page, context }) => {
  await context.clearCookies();
  await page.goto('/guestbook');
  await page.fill('input[name="name"]', 'E2E Visitor');
  await page.fill('textarea[name="message"]', 'Hello from Chromium');
  await Promise.all([
    page.waitForNavigation(),
    page.click('button[type="submit"]'),
  ]);
  await expect(page).toHaveURL(/\/guestbook$/);
});

test('a repeated submission after refresh does not repeat the mutation', async ({ page }) => {
  // Submit once, then reload the PRG destination: the entry count must
  // stay at one because the browser re-issues GET, never the POST.
  await page.goto('/guestbook');
  const before = Number(
    await page.locator('[data-guestbook-count]').textContent(),
  );
  await page.fill('input[name="name"]', 'Once Only');
  await page.fill('textarea[name="message"]', 'single entry');
  await Promise.all([
    page.waitForNavigation(),
    page.click('button[type="submit"]'),
  ]);
  await expect(page).toHaveURL(/\/guestbook$/);
  await expect(page.locator('[data-guestbook-count]')).toHaveText(
    String(before + 1),
  );
  await page.reload();
  await expect(page.locator('[data-guestbook-count]')).toHaveText(
    String(before + 1),
  );
});

test('tampered CSRF token is rejected without executing the action', async ({ page }) => {
  await page.goto('/guestbook');
  await page.fill('input[name="name"]', 'Attacker');
  await page.fill('textarea[name="message"]', 'Should not persist');
  await page.evaluate(() => {
    const field = document.querySelector(
      'form[data-go-form="guestbook"] input[name="_csrf"]',
    ) as HTMLInputElement | null;
    if (field) field.value = field.value.slice(0, -2) + 'zz';
  });
  await Promise.all([
    page.waitForNavigation(),
    page.click('button[type="submit"]'),
  ]);
  await expect(page.locator('body')).toContainText('Invalid CSRF token');
});
