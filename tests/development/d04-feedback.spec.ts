import { test, expect } from '@playwright/test';
import { newDevSession, read, write, pause } from './fixtures/dev-session';

test('validation feedback and decoder edits rebuild and recover', async ({ page, request }) => {
  const session = await newDevSession();
  try {
    await session.start();
    await session.waitUntil(request, '/guestbook', 200);
    const origin = session.base.replace('127.0.0.1', 'localhost');
    const form = 'app/guestbookform/form.templ';
    const actions = 'app/actions/bindings.go';
    const originalForm = read(session.root, form);
    const originalActions = read(session.root, actions);
    write(session.root, form, originalForm.replace('Sign guestbook', 'Submit revised guestbook'));
    write(session.root, actions, originalActions.replace('Name is required', 'Revised name required').replace('names := values["name"]', 'names := values["name"] // revised decoder'));
    await expect.poll(async () => { try { return (await request.get(`${origin}/guestbook`)).text(); } catch { return ''; } }, { timeout: 60000 }).toContain('Submit revised guestbook');
    await page.goto(`${origin}/guestbook`);
    await page.locator('[name="name"]').fill(' ');
    await page.locator('[name="message"]').fill('hello');
    await page.getByRole('button', { name: 'Submit revised guestbook' }).click();
    await expect(page.getByText('Revised name required')).toBeVisible();
    write(session.root, actions, read(session.root, actions) + '\nthis is invalid Go\n');
    await expect.poll(() => session.output, { timeout: 60000 }).toContain('syntax error');
    const runs = (session.output.match(/running\.\.\./g) ?? []).length;
    write(session.root, actions, read(session.root, actions).replace('\nthis is invalid Go\n', '\n'));
    await expect.poll(() => (session.output.match(/running\.\.\./g) ?? []).length, { timeout: 60000 }).toBeGreaterThan(runs);
    await expect.poll(async () => { try { return (await request.get(`${origin}/guestbook`)).status(); } catch { return 0; } }, { timeout: 60000 }).toBe(200);
    const builds = (session.output.match(/building\.\.\./g) ?? []).length;
    await pause(3000);
    expect((session.output.match(/building\.\.\./g) ?? []).length).toBe(builds);
  } finally {
    await session.stop();
    expect(session.processesGone()).toBe(true);
    session.dispose();
  }
});
