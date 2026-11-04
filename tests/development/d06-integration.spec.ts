import { test, expect } from '@playwright/test';
import { newDevSession, read, write, pause } from './fixtures/dev-session';

// P2-D06 development integration: the actual compiled Svelte form fixture
// (app/d06/form/page.svelte) through meta dev — Vite HMR edit of the form
// component, rebuild after an action-binding edit, and a duplicate action ID
// startup diagnostic that must fail loudly and recover.
test('the compiled Svelte form fixture works in meta dev and survives edits', async ({ page, request }) => {
  const session = await newDevSession();
  try {
    await session.start();
    await session.waitUntil(request, '/', 200);
    const origin = session.base.replace('127.0.0.1', 'localhost');
    // The route registry regenerates when fixtures change; the dev server
    // boots with the current generated routes.
    await page.goto(`${origin}/d06/form`);
    await expect(page.locator('[data-go-page="d06-form"]')).toBeVisible({ timeout: 30_000 });
    await page.locator('#d06-name').fill(' ');
    await page.locator('#d06-message').fill('dev form');
    const result = page.waitForResponse((r) => r.url().includes('/_meta/actions/guestbook.submit'));
    await page.getByRole('button', { name: 'Submit locally' }).click();
    const response = await result;
    expect(response.status()).toBe(422);
    await expect(page.getByText('Name is required')).toBeVisible();
    // Edit the Svelte component: Vite HMR updates the form in place.
    const svelte = 'app/d06/form/page.svelte';
    write(session.root, svelte, read(session.root, svelte).replace('Submit locally', 'Submit edited'));
    await expect(page.getByRole('button', { name: 'Submit edited' })).toBeVisible({ timeout: 30_000 });
    // No idle rebuild loop after the edit settles.
    const builds = (session.output.match(/building\.\.\./g) ?? []).length;
    await pause(3000);
    expect((session.output.match(/building\.\.\./g) ?? []).length).toBe(builds);
  } finally {
    await session.stop();
    expect(session.processesGone()).toBe(true);
    session.dispose();
  }
});

test('a duplicate action id fails at startup with an actionable diagnostic', async ({ request }) => {
  const session = await newDevSession();
  try {
    await session.start();
    await session.waitUntil(request, '/', 200);
    const actions = 'app/actions/bindings.go';
    const original = read(session.root, actions);
    // Register guestbook.submit twice: registry construction must fail
    // atomically and the dev server must report it instead of starting
    // half-configured.
    write(session.root, actions, original.replace(
      'action.BindForm("guestbook.submit"',
      'action.Bind("guestbook.submit", SubmitGuestbook),\n\t\taction.BindForm("guestbook.submit"'));
    await expect.poll(() => session.output, { timeout: 60_000 }).toContain('duplicate');
    // Correcting the binding recovers without orphan processes.
    write(session.root, actions, original);
    await expect.poll(async () => { try { return (await request.get(`${session.base.replace('127.0.0.1', 'localhost')}/`)).status(); } catch { return 0; } }, { timeout: 60_000 }).toBe(200);
  } finally {
    await session.stop();
    expect(session.processesGone()).toBe(true);
    session.dispose();
  }
});
