import { expect, test } from '@playwright/test';

test('enhanced form validates without reload and then navigates on success', async ({ page }) => {
  await page.goto('/guestbook');
  await page.evaluate(() => { (window as unknown as { d05Marker: string }).d05Marker = 'same document'; });
  await page.locator('[name="name"]').fill(' ');
  await page.locator('[name="message"]').fill('Already typed <text>');
  const invalid = page.waitForResponse((r) => r.url().includes('/_meta/actions/guestbook.submit') && r.request().method() === 'POST');
  await page.getByRole('button', { name: 'Sign guestbook' }).click();
  expect((await invalid).status()).toBe(422);
  await expect(page.getByText('Name is required')).toBeVisible();
  await expect(page.locator('[name="name"]')).toHaveAttribute('aria-invalid', 'true');
  const id = await page.locator('[name="name"]').getAttribute('aria-describedby');
  await expect(page.locator(`[id="${id}"]`)).toContainText('Name is required');
  await expect(page.locator('[name="message"]')).toHaveValue('Already typed <text>');
  expect(await page.evaluate(() => (window as unknown as { d05Marker: string }).d05Marker)).toBe('same document');
  await page.locator('[name="name"]').fill('Alice');
  const done = page.waitForNavigation({ url: '**/guestbook' });
  const success = page.waitForResponse((r) => r.url().includes('/_meta/actions/guestbook.submit') && r.request().method() === 'POST');
  await page.getByRole('button', { name: 'Sign guestbook' }).click();
  expect((await success).status()).toBe(200);
  await done;
  await expect(page.locator('[data-guestbook-count]')).toBeVisible();
});

test('a pending form ignores a second trusted click', async ({ page }) => {
  await page.goto('/guestbook');
  let release!: () => void;
  const blocked = new Promise<void>((resolve) => { release = resolve; });
  let posts = 0;
  await page.route('**/_meta/actions/guestbook.submit', async (route) => { posts++; await blocked; await route.continue(); });
  await page.locator('[name="name"]').fill('Alice');
  await page.locator('[name="message"]').fill('Hello');
  await page.getByRole('button', { name: 'Sign guestbook' }).click();
  await expect(page.locator('form[data-go-meta-enhance]')).toHaveAttribute('aria-busy', 'true');
  await page.getByRole('button', { name: 'Sign guestbook' }).click();
  expect(posts).toBe(1);
  const response = page.waitForResponse((r) => r.url().includes('/_meta/actions/guestbook.submit'));
  release(); await response;
});

test('a form mounted in a CSR page uses the same handler', async ({ page }) => {
  await page.goto('/dashboard');
  await page.locator('[data-go-page="dashboard"]').waitFor();
  await page.evaluate(() => {
    const token = decodeURIComponent(document.cookie.split('; ').find((c) => c.startsWith('_meta_csrf='))!.split('=')[1]);
    const form = document.createElement('form');
    form.method = 'post'; form.action = '/_meta/actions/guestbook.submit'; form.setAttribute('data-go-meta-enhance', '');
    for (const [name, value] of [['_csrf', token], ['name', ' '], ['message', 'CSR form']]) {
      const input = document.createElement('input'); input.name = name; input.value = value; form.append(input);
    }
    const button = document.createElement('button'); button.textContent = 'Submit CSR'; form.append(button);
    document.querySelector('[data-go-page="dashboard"]')!.append(form);
  });
  const response = page.waitForResponse((r) => r.url().includes('/_meta/actions/guestbook.submit'));
  await page.getByRole('button', { name: 'Submit CSR' }).click();
  expect((await response).status()).toBe(422);
  await expect(page.getByText('Name is required')).toBeVisible();
});

test('enhancement does not bypass a tampered CSRF token', async ({ page }) => {
  await page.goto('/guestbook');
  await page.locator('[name="name"]').fill('Alice');
  await page.locator('[name="message"]').fill('Hello');
  await page.locator('[name="_csrf"]').evaluate((el: HTMLInputElement) => { el.value = 'invalid'; });
  const response = page.waitForResponse((r) => r.url().includes('/_meta/actions/guestbook.submit'));
  await page.getByRole('button', { name: 'Sign guestbook' }).click();
  expect((await response).status()).toBe(403);
  await expect(page.getByText('The submission could not be confirmed. Check its result before trying again.')).toBeVisible();
});

test('enhancement works after trusted soft navigation to an SSR form', async ({ page }) => {
  await page.goto('/about');
  await page.evaluate(() => { const a = document.createElement('a'); a.href = '/guestbook'; a.textContent = 'Open guestbook'; document.body.append(a); });
  await page.getByRole('link', { name: 'Open guestbook' }).click();
  await expect(page).toHaveURL(/\/guestbook$/);
  await page.locator('[name="name"]').fill(' ');
  await page.locator('[name="message"]').fill('hello');
  const response = page.waitForResponse((r) => r.url().includes('/_meta/actions/guestbook.submit'));
  await page.getByRole('button', { name: 'Sign guestbook' }).click();
  expect((await response).status()).toBe(422);
  await expect(page.getByText('Name is required')).toBeVisible();
});

test('a late response from one form cannot affect a successful second form', async ({ page }) => {
  await page.goto('/guestbook');
  await page.evaluate(() => {
    const original = document.querySelector('form[data-go-meta-enhance]')!;
    const clone = original.cloneNode(true) as HTMLFormElement;
    clone.dataset.testForm = 'second';
    original.insertAdjacentElement('afterend', clone);
  });
  const first = page.locator('form[data-go-meta-enhance]').first();
  const second = page.locator('form[data-test-form="second"]');
  let release!: () => void;
  const blocked = new Promise<void>((resolve) => { release = resolve; });
  await page.route('**/_meta/actions/guestbook.submit', async (route) => {
    if (new URLSearchParams(route.request().postData() ?? '').get('name') === ' ') await blocked;
    try { await route.continue(); } catch { /* The old request may have been aborted. */ }
  });
  await first.locator('[name="name"]').fill(' ');
  await first.locator('[name="message"]').fill('first');
  await second.locator('[name="name"]').fill('Second');
  await second.locator('[name="message"]').fill('second');
  await first.getByRole('button', { name: 'Sign guestbook' }).click();
  await expect(first).toHaveAttribute('aria-busy', 'true');
  const done = page.waitForNavigation({ url: '**/guestbook' });
  const secondResponse = page.waitForResponse((r) => r.url().includes('/_meta/actions/guestbook.submit') && new URLSearchParams(r.request().postData() ?? '').get('name') === 'Second');
  await second.getByRole('button', { name: 'Sign guestbook' }).click();
  expect((await secondResponse).status()).toBe(200);
  release(); await done;
  await expect(page.locator('[data-guestbook-count]')).toBeVisible();
  await expect(page.getByText('Name is required')).toHaveCount(0);
});

test('browser serialization includes the submitter and repeated values', async ({ page }) => {
  await page.goto('/guestbook');
  await page.locator('[name="name"]').fill(' ');
  await page.locator('[name="message"]').fill('first\nsecond');
  await page.evaluate(() => {
    const form = document.querySelector('form[data-go-meta-enhance]')!;
    const button = form.querySelector('button')!; button.name = 'intent'; button.value = 'save';
    for (const value of ['one', 'two']) { const input = document.createElement('input'); input.name = 'tags'; input.value = value; form.append(input); }
  });
  const request = page.waitForRequest((r) => r.url().includes('/_meta/actions/guestbook.submit') && r.method() === 'POST');
  await page.getByRole('button', { name: 'Sign guestbook' }).click();
  const data = new URLSearchParams((await request).postData() ?? '');
  expect(data.get('intent')).toBe('save');
  expect(data.getAll('tags')).toEqual(['one', 'two']);
  expect(data.get('message')).toBe('first\r\nsecond');
  expect(data.get('_csrf')).toBeTruthy();
});
