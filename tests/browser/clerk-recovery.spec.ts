import { test, expect, type Route } from '@playwright/test';

// Real Meta Go server/JWT verification + simulated ClerkJS, never a live tenant.
test.use({ baseURL: 'http://localhost:3135' });

for (const mode of ['document', 'soft'] as const) test(`expired returning user recovers without sign-in: ${mode}`, async ({ page }) => {
  await page.request.get('/fixture/token?kind=expired');
  const signIns: string[] = [];
  page.on('request', request => { if (new URL(request.url()).pathname === '/sign-in') signIns.push('sign-in'); });
  let refresh: Route | undefined;
  await page.route('**/fixture/refresh', route => { refresh = route; });
  if (mode === 'soft') {
    let initializations = 0;
    page.on('request', request => { if (new URL(request.url()).pathname === '/fixture/client') initializations++; });
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'Landing' })).toBeVisible();
    expect(initializations).toBe(0); // SSR landing does not initialize Clerk.
    await page.evaluate(() => { (window as any).__sameDocument = true; });
    await page.click('a[href="/dashboard?tab=team"]');
  } else {
    await page.goto('/dashboard?tab=team');
  }
  await expect(page.getByRole('status')).toHaveText('Checking your session…');
  await expect.poll(() => Boolean(refresh)).toBe(true);
  await expect(page).toHaveURL(/\/auth\/recover\?/);
  await refresh!.continue();
  await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible();
  await expect(page).toHaveURL('http://localhost:3135/dashboard?tab=team');
  expect(signIns).toEqual([]);
  if (mode === 'soft') expect(await page.evaluate(() => (window as any).__sameDocument)).toBe(true);
});

test('fresh valid session directly renders protected SSR without recovery', async ({ page }) => {
  await page.request.get('/fixture/token?kind=valid');
  const response = await page.goto('/dashboard');
  expect(response?.status()).toBe(200);
  await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible();
  await expect(page).toHaveURL('http://localhost:3135/dashboard');
});

test('genuinely signed-out browser goes to interactive sign-in with its intended destination', async ({ page, context }) => {
  await context.addCookies([{ name: 'fixture_mode', value: 'signed-out', url: 'http://localhost:3135' }]);
  await page.goto('/dashboard?tab=team');
  await expect(page.getByRole('heading', { name: 'Interactive sign-in' })).toBeVisible();
  expect(new URL(page.url()).searchParams.get('redirect_url')).toBe('/dashboard?tab=team');
});

test('forged token never authenticates or enters recovery', async ({ page }) => {
  await page.request.get('/fixture/token?kind=forged');
  let recoveries = 0;
  page.on('request', request => { if (new URL(request.url()).pathname === '/auth/recover') recoveries++; });
  await page.goto('/dashboard');
  await expect(page.getByRole('heading', { name: 'Interactive sign-in' })).toBeVisible();
  expect(recoveries).toBe(0);
});

test('signing-key outage stays a generic 503, without redirecting to sign-in', async ({ page }) => {
  await page.request.get('/fixture/token?kind=expired');
  const response = await page.goto('/dashboard?outage=1');
  expect(response?.status()).toBe(503);
  await expect(page.locator('body')).toHaveText('Authentication temporarily unavailable');
  await expect(page).toHaveURL('http://localhost:3135/dashboard?outage=1');
});

test('refresh failure has an explicit retry that recovers without losing the destination', async ({ page, context }) => {
  await page.request.get('/fixture/token?kind=expired');
  await context.addCookies([{ name: 'fixture_mode', value: 'refresh-failure', url: 'http://localhost:3135' }]);
  await page.goto('/dashboard?tab=team');
  await expect(page.getByRole('alert')).toContainText('try again');
  await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible();
  await context.clearCookies({ name: 'fixture_mode' });
  await page.click('button');
  await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible();
  await expect(page).toHaveURL('http://localhost:3135/dashboard?tab=team');
});

test('unchanged cookie fails server revalidation; a failed resume cannot automatically loop', async ({ page, context }) => {
  await page.request.get('/fixture/token?kind=expired');
  await context.addCookies([{ name: 'fixture_mode', value: 'stale-refresh', url: 'http://localhost:3135' }]);
  await page.goto('/dashboard');
  await expect(page.getByRole('alert')).toBeVisible();
  // Simulate an erroneous caller resuming anyway. Server returns a manual-retry page.
  await page.goto('/dashboard');
  await expect(page.getByRole('alert')).toBeVisible();
  expect(new URL(page.url()).searchParams.get('retry')).toBe('1');
  await expect(page.getByRole('status')).toHaveCount(0);
});

test('reuses a loaded Clerk instance and never creates another browser SDK', async ({ page }) => {
  await page.request.get('/fixture/token?kind=expired');
  await page.addInitScript(() => {
    (window as any).__refreshCalls = 0;
    (window as any).Clerk = { loaded: true, session: { async getToken(options: { skipCache: boolean }) {
      if (!options.skipCache) throw new Error('must force refresh');
      (window as any).__refreshCalls++;
      await fetch('/fixture/refresh');
      return 'fixture-not-used-as-proof';
    } } };
  });
  let created = 0;
  page.on('request', request => { if (new URL(request.url()).pathname === '/fixture/client') created++; });
  await page.goto('/dashboard');
  await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible();
  expect(created).toBe(0);
  expect(await page.evaluate(() => (window as any).__refreshCalls)).toBe(1);
});

test('rejects foreign/recursive redirect destinations and exposes only redacted diagnostics', async ({ page }) => {
  for (const target of ['https://evil.invalid', '//evil.invalid', '/%5cevil.invalid', '/auth/recover', '/x/../auth/recover', '/sign-in']) {
    const response = await page.request.get('/auth/recover?return_to=' + encodeURIComponent(target), { maxRedirects: 0 });
    expect(response.status()).toBe(400);
    expect(response.headers().location).toBeUndefined();
  }
  await page.request.get('/fixture/token?kind=expired');
  await page.request.get('/dashboard', { maxRedirects: 0 });
  const response = await page.request.get('/fixture/diagnostics');
  const diagnostics = await response.text();
  for (const secret of ['sk_test_', '__session', 'fixture-user', 'fixture-session', 'eyJ']) expect(diagnostics).not.toContain(secret);
  expect(diagnostics).toContain('expired_token');
});
