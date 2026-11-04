import { test, expect } from '@playwright/test';
import { newDevSession, read, write, pause } from './fixtures/dev-session';

test('enhanced forms reload and recover in meta dev', async ({ page, request }) => {
  const session = await newDevSession();
  try {
    await session.start();
    await session.waitUntil(request, '/guestbook', 200);
    const origin = session.base.replace('127.0.0.1', 'localhost');
    const templ = 'app/guestbookform/form.templ';
    const go = 'app/actions/bindings.go';
    write(session.root, templ, read(session.root, templ).replace('Sign guestbook', 'Submit updated'));
    write(session.root, go, read(session.root, go).replace('Name is required', 'Updated validation'));
    await expect.poll(async () => { try { return (await request.get(`${origin}/guestbook`)).text(); } catch { return ''; } }, { timeout: 60000 }).toContain('Submit updated');
    await page.goto(`${origin}/guestbook`);
    await page.locator('[name="name"]').fill(' ');
    await page.locator('[name="message"]').fill('hello');
    const result = page.waitForResponse((r) => r.url().includes('/_meta/actions/guestbook.submit'));
    await page.getByRole('button', { name: 'Submit updated' }).click();
    const response = await result; expect(response.status()).toBe(422); expect(response.headers()['content-type']).toContain('application/json');
    await expect(page.getByText('Updated validation')).toBeVisible();
    const original = read(session.root, go);
    write(session.root, go, original + '\nthis is invalid Go\n');
    await expect.poll(() => session.output, { timeout: 60000 }).toContain('syntax error');
    const runs = (session.output.match(/running\.\.\./g) ?? []).length;
    write(session.root, go, original);
    await expect.poll(() => (session.output.match(/running\.\.\./g) ?? []).length, { timeout: 60000 }).toBeGreaterThan(runs);
    await page.goto(`${origin}/dashboard`);
    await expect(page.locator('[data-go-page="dashboard"]')).toBeVisible();
    const svelte = 'app/dashboard/page.svelte';
    write(session.root, svelte, read(session.root, svelte).replace('</main>', '<form method="post" action="/_meta/actions/guestbook.submit" data-go-meta-enhance><button>HMR form</button></form></main>'));
    await expect(page.getByRole('button', { name: 'HMR form' })).toBeVisible({ timeout: 30000 });
    await pause(3000);
    const builds = (session.output.match(/building\.\.\./g) ?? []).length;
    await pause(3000);
    expect((session.output.match(/building\.\.\./g) ?? []).length).toBe(builds);
  } finally {
    await session.stop();
    expect(session.processesGone()).toBe(true);
    session.dispose();
  }
});
