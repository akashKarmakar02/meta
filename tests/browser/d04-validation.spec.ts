import { test, expect } from '@playwright/test';

test.use({ javaScriptEnabled: false });

test('native validation, correction and PRG', async ({ page }) => {
  await page.goto('/guestbook');
  await page.locator('[name="name"]').fill(' ');
  await page.locator('[name="message"]').fill('Previously entered <message>');
  await page.getByRole('button', { name: 'Sign guestbook' }).click();
  await expect(page.getByText('Name is required')).toBeVisible();
  await expect(page.locator('[name="name"]')).toHaveAttribute('aria-invalid', 'true');
  await expect(page.locator('[name="name"]')).toHaveAttribute('aria-describedby', 'guestbook-name-error');
  await expect(page.locator('[name="message"]')).toHaveValue('Previously entered <message>');
  await page.locator('[name="name"]').fill('Alice');
  await page.getByRole('button', { name: 'Sign guestbook' }).click();
  await expect(page).toHaveURL(/\/guestbook$/);
  const count = await page.locator('[data-guestbook-count]').textContent();
  await page.reload();
  await expect(page.locator('[data-guestbook-count]')).toHaveText(count!);
});
