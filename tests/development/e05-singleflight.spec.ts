import { test, expect } from '@playwright/test';
import { newDevSession, read, write } from './fixtures/dev-session';

test('singleflight configuration rebuilds and recovers in development', async ({ page, request }) => {
  const session = await newDevSession();
  try {
    await session.start();
    await session.waitUntil(request, '/cache-demo/post/e05-ready', 200);
    const file = 'cmd/example/main.go';
    const original = read(session.root, file);
    const invalid = original.replace('demoInvalidator, 5*time.Second', 'demoInvalidator, 0*time.Second');
    expect(invalid).not.toBe(original);
    write(session.root, file, invalid);
    await expect.poll(() => session.output, { timeout: 60000 }).toContain('positive timeout');
    write(session.root, file, original);
    await expect.poll(async () => { try { return (await request.get(`${session.base}/cache-demo/post/e05-recovered`)).status(); } catch { return 0; } }, { timeout: 60000 }).toBe(200);
    const other = await page.context().newPage();
    try {
      const url = `${session.base}/cache-demo/post/e05-concurrent`;
      await Promise.all([page.goto(`${url}?slow=1&key=shared`), other.goto(`${url}?key=shared&slow=1`)]);
      const value = await page.locator('[data-cache-refreshed]').textContent();
      expect(value).toBeTruthy();
      await expect(other.locator('[data-cache-refreshed]')).toHaveText(value!);
      const runs = (session.output.match(/running\.\.\./g) ?? []).length;
      const disabled = original.replace('bindings := append(loaders.Bindings(), loaders.CachedDemoBindingsWithFlights(demoStore, demoInvalidator, demoFlights)...)', '_ = demoFlights\n\tbindings := append(loaders.Bindings(), loaders.CachedDemoBindings(demoStore, demoInvalidator)...)');
      expect(disabled).not.toBe(original);
      write(session.root, file, disabled);
      await expect.poll(() => (session.output.match(/running\.\.\./g) ?? []).length, { timeout: 60000 }).toBeGreaterThan(runs);
      const disabledURL = `${session.base}/cache-demo/post/e05-disabled`;
      await Promise.all([page.goto(`${disabledURL}?slow=1&key=shared`), other.goto(`${disabledURL}?key=shared&slow=1`)]);
      const unshared = await page.locator('[data-cache-refreshed]').textContent();
      await expect(other.locator('[data-cache-refreshed]')).not.toHaveText(unshared!);
    } finally { await other.close(); }
  } finally {
    await session.stop(); expect(session.processesGone()).toBe(true); session.dispose();
  }
});
