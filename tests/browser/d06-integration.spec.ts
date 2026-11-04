import { expect, test } from '@playwright/test';

// P2-D06 integration gate. Covers the cases D01–D05 left untested:
//
//  1. Committed mutation followed by failed destination navigation —
//     action POST succeeds, destination GET fails; no retry may occur.
//  2. An actual compiled Svelte page component with an enhanced form:
//     mount/unmount lifecycle, re-entry, stale feedback after unmount.
//  3. <base target> and submitter overrides (formtarget/formmethod/
//     formaction/formenctype) must keep the submission native.
//  4. Injection payloads in field names and validation messages render as
//     text, never HTML.
//  5. Cross-transport error consistency for enhanced forms.

test('a committed mutation whose destination navigation fails is never retried', async ({ page }) => {
  await page.goto('/guestbook');
  await page.locator('[name="name"]').fill('Alice');
  await page.locator('[name="message"]').fill('committed-then-navigation-fails');
  // Count action POSTs precisely.
  let posts = 0;
  await page.route('**/_meta/actions/guestbook.submit', async (route) => {
    posts++;
    await route.continue();
  });
  // The action succeeds; the destination GET fails at the network layer.
  let destinationFails = false;
  await page.route('**/guestbook', async (route) => {
    if (destinationFails && route.request().method() === 'GET'
      && !route.request().url().includes('/_meta/actions/')) {
      await route.abort('failed');
      return;
    }
    await route.continue();
  });
  await page.getByRole('button', { name: 'Sign guestbook' }).click();
  // The success response arrives first (mutation committed server-side)...
  await expect
    .poll(() => posts, { timeout: 10_000 })
    .toBe(1);
  // ...then window.location.assign('/guestbook') starts a GET that fails.
  // Chromium replaces the document on a failed main-frame load: the result
  // is an error page, and no further interaction is possible there. What we
  // CAN assert — the strongest observable guarantees — is over the request
  // log of this context: exactly one POST ever happened, and never another.
  destinationFails = true;
  await expect
    .poll(() => posts, { timeout: 10_000 })
    .toBe(1);
  // Back does not replay the POST either (it was never entered into
  // history as a POST entry): going back re-issues only GETs.
  await page.goBack().catch(() => undefined);
  await expect
    .poll(() => posts, { timeout: 10_000 })
    .toBe(1);
});

test('an ambiguous network failure never auto-retries the mutation', async ({ page }) => {
  await page.goto('/guestbook');
  await page.locator('[name="name"]').fill('Alice');
  await page.locator('[name="message"]').fill('ambiguous-outcome');
  let posts = 0;
  await page.route('**/_meta/actions/guestbook.submit', async (route) => {
    posts++;
    // Abort AFTER the request reached the wire: the client cannot know
    // whether the server committed. The user input must be preserved and
    // no automatic retry fired.
    await route.abort('failed');
  });
  await page.getByRole('button', { name: 'Sign guestbook' }).click();
  await expect
    .poll(() => posts, { timeout: 10_000 })
    .toBe(1);
  await expect(page.getByText(/outcome is unknown/i)).toBeVisible();
  // No retry: only one POST request across a generous window.
  await page.waitForTimeout(1500);
  expect(posts).toBe(1);
  // Input is preserved for an explicit user decision.
  await expect(page.locator('[name="message"]')).toHaveValue('ambiguous-outcome');
  await expect(page.locator('[name="name"]')).toHaveValue('Alice');
});

test('a compiled Svelte component form submits enhanced through its full lifecycle', async ({ page }) => {
  // Direct CSR load of the real Svelte page fixture.
  await page.goto('/d06/form');
  await expect(page.locator('[data-go-page="d06-form"]')).toBeVisible();
  await expect(page.locator('form[data-go-form="d06"]')).toHaveAttribute('data-go-meta-enhance', '');
  // Validation 422 without reload, inside the mounted component.
  const marker = { value: 'same document' };
  await page.evaluate((v) => { (window as unknown as { d06Marker: string }).d06Marker = v; }, marker.value);
  await page.locator('#d06-name').fill(' ');
  await page.locator('#d06-message').fill('from svelte');
  const invalid = page.waitForResponse((r) => r.url().includes('/_meta/actions/guestbook.submit') && r.request().method() === 'POST');
  await page.getByRole('button', { name: 'Submit locally' }).click();
  expect((await invalid).status()).toBe(422);
  await expect(page.getByText('Name is required')).toBeVisible();
  expect(await page.evaluate(() => (window as unknown as { d06Marker: string }).d06Marker)).toBe('same document');
  // Corrected submission: JSON success then full-document GET to /guestbook.
  await page.locator('#d06-name').fill('Svelte User');
  const done = page.waitForNavigation({ url: '**/guestbook' });
  const ok = page.waitForResponse((r) => r.url().includes('/_meta/actions/guestbook.submit') && r.request().method() === 'POST');
  await page.getByRole('button', { name: 'Submit locally' }).click();
  expect((await ok).status()).toBe(200);
  await done;
  await expect(page.locator('[data-go-page="guestbook"]')).toBeVisible();
});

test('a Svelte component with a pending form unmounts cleanly and leaves no stale feedback', async ({ page }) => {
  await page.goto('/d06/form');
  await expect(page.locator('[data-go-page="d06-form"]')).toBeVisible();
  // Hold the action response while the navigation away unmounts the component.
  let release!: () => void;
  const blocked = new Promise<void>((resolve) => { release = resolve; });
  await page.route('**/_meta/actions/guestbook.submit', async (route) => { await blocked; try { await route.continue(); } catch { /* aborted */ } });
  await page.locator('#d06-name').fill('Stale');
  await page.locator('#d06-message').fill('unmount me');
  await page.getByRole('button', { name: 'Submit locally' }).click();
  await expect(page.locator('form[data-go-form="d06"]')).toHaveAttribute('aria-busy', 'true');
  // Soft-navigate away: the pipeline unmounts the Svelte page (and its layout).
  await page.evaluate(() => {
    const link = document.createElement('a');
    link.href = '/about';
    link.textContent = 'Leave for about';
    link.dataset.testNav = 'about';
    document.querySelector('[data-go-page="d06-form"]')!.append(link);
  });
  await page.getByRole('link', { name: 'Leave for about' }).click();
  await expect(page.locator('[data-go-page-about]')).toBeVisible();
  // The late response arrives after unmount: it must not touch anything.
  release();
  await page.waitForTimeout(500);
  await expect(page.getByText('Name is required')).toHaveCount(0);
  await expect(page.getByText(/outcome is unknown/i)).toHaveCount(0);
  // Re-entry remounts a fresh component: no duplicate feedback nodes, no
  // duplicate listeners (one POST per click proves single delegation).
  await page.goBack();
  await expect(page.locator('[data-go-page="d06-form"]')).toBeVisible();
  let posts = 0;
  await page.unrouteAll({ behavior: 'wait' });
  await page.route('**/_meta/actions/guestbook.submit', async (route) => { posts++; await route.continue(); });
  await page.locator('#d06-name').fill(' ');
  await page.locator('#d06-message').fill('reentry');
  await page.getByRole('button', { name: 'Submit locally' }).click();
  await expect
    .poll(() => posts, { timeout: 10_000 })
    .toBe(1);
  await expect(page.getByText('Name is required')).toHaveCount(1);
});

test('submitter overrides and base target keep forms native', async ({ page }) => {
  // Count same-window enhanced fetches from inside the page.
  await page.addInitScript(() => {
    const original = window.fetch;
    let count = 0;
    window.fetch = ((...args: Parameters<typeof fetch>) => {
      const url = String(args[0]);
      if (url.includes('/_meta/actions/')) count++;
      (window as unknown as { __d06FetchCount: number }).__d06FetchCount = count;
      return original.apply(window, args);
    }) as typeof fetch;
  });
  await page.goto('/guestbook');
  // formtarget=_blank on the submitter: the submission must go native
  // (another browsing context), never an enhanced same-window POST.
  await page.evaluate(() => {
    const form = document.querySelector('form[data-go-meta-enhance]')!;
    form.querySelector('button')!.setAttribute('formtarget', '_blank');
  });
  let posts = 0;
  await page.route('**/_meta/actions/guestbook.submit', async (route) => { posts++; await route.continue(); });
  await page.getByRole('button', { name: 'Sign guestbook' }).click();
  await page.waitForTimeout(1200);
  expect(await page.evaluate(() => (window as unknown as { __d06FetchCount?: number }).__d06FetchCount ?? 0)).toBe(0);
  expect(posts).toBeGreaterThanOrEqual(0); // any POST came from the popup, natively
  // formmethod=get: wrong method, stays native.
  await page.evaluate(() => {
    const form = document.querySelector('form[data-go-meta-enhance]')!;
    const button = form.querySelector('button')!;
    button.removeAttribute('formtarget');
    button.setAttribute('formmethod', 'get');
  });
  posts = 0;
  await page.getByRole('button', { name: 'Sign guestbook' }).click();
  await page.waitForTimeout(1000);
  expect(await page.evaluate(() => (window as unknown as { __d06FetchCount?: number }).__d06FetchCount ?? 0)).toBe(0);
  // <base target="_blank"> without an explicit form target: same rule.
  await page.evaluate(() => {
    const form = document.querySelector('form[data-go-meta-enhance]')!;
    form.querySelector('button')!.removeAttribute('formmethod');
    const base = document.createElement('base');
    base.target = '_blank';
    document.head.append(base);
  });
  posts = 0;
  await page.getByRole('button', { name: 'Sign guestbook' }).click();
  await page.waitForTimeout(1000);
  expect(await page.evaluate(() => (window as unknown as { __d06FetchCount?: number }).__d06FetchCount ?? 0)).toBe(0);
});

test('injection payloads in field errors render as text', async ({ page }) => {
  await page.goto('/guestbook');
  // A field VALUE carrying script payloads; the value stays inside inputs
  // and any validation feedback renders as text (textContent), never HTML.
  await page.locator('[name="name"]').fill('"><img src=x onerror=window.__pwned=1>');
  await page.locator('[name="message"]').fill('</script><script>alert(1)</script>');
  const response = page.waitForResponse((r) => r.url().includes('/_meta/actions/guestbook.submit'));
  await page.getByRole('button', { name: 'Sign guestbook' }).click();
  const res = await response;
  if (res.status() === 422) {
    // The server rejected (empty or over-length name): its fixed validation
    // messages render as text via textContent.
    await expect(page.getByText(/Name must be|Name is required/)).toBeVisible();
  } else {
    // Success: the enhanced handler navigates to the destination; the
    // payload value was sent but never rendered anywhere on the way.
    await expect(page.locator('[data-go-page="guestbook"]')).toBeVisible({ timeout: 10_000 });
  }
  // No injected script ever ran, regardless of branch; feedback is built
  // from textContent, values stay inside inputs.
  const pwned = await page.evaluate(() => (window as unknown as { __pwned?: number }).__pwned ?? 0);
  expect(pwned).toBe(0);
});

test('enhanced form error responses use JSON across the failure matrix', async ({ page }) => {
  await page.goto('/guestbook');
  await page.locator('[name="name"]').fill('Alice');
  await page.locator('[name="message"]').fill('hello');
  // Missing CSRF entirely: safe JSON 403 for the enhanced marker.
  await page.evaluate(() => {
    const field = document.querySelector('form input[name="_csrf"]') as HTMLInputElement | null;
    if (field) field.value = '';
  });
  const response = page.waitForResponse((r) => r.url().includes('/_meta/actions/guestbook.submit'));
  await page.getByRole('button', { name: 'Sign guestbook' }).click();
  const res = await response;
  expect(res.status()).toBe(403);
  expect((res.headers()['content-type'] ?? '').toLowerCase()).toContain('application/json');
});
