import { test, expect } from '@playwright/test';
import { newDevSession, read, write, pause } from './fixtures/dev-session';

test('cached loader configuration rebuilds, resets, and recovers', async ({ page, request }) => {
  const session = await newDevSession();
  try {
    await session.start();
    await session.waitUntil(request, '/cache-demo/post/1?tab=first', 200);
    const url = `${session.base}/cache-demo/post/1?tab=first`;
    await page.goto(url);
    const before = await page.locator('[data-cache-refreshed]').textContent();
    await page.reload(); await expect(page.locator('[data-cache-refreshed]')).toHaveText(before!);
    const file = 'app/loaders/cache_demo.go';
    const original = read(session.root, file);
    const changed = original.replace('TTL: 2 * time.Second', 'TTL: time.Hour').replace('Tab: route.Query.Get("tab")', 'Tab: "updated-" + route.Query.Get("tab")');
    write(session.root, file, changed);
    await expect.poll(async () => { try { return (await request.get(url)).text(); } catch { return ''; } }, { timeout: 60000 }).toContain('updated-first');
    await page.reload(); await expect(page.locator('[data-cache-tab]')).toHaveText('updated-first');
    const after = await page.locator('[data-cache-refreshed]').textContent();
    expect(after).not.toBe(before);
    await pause(2200); await page.reload(); await expect(page.locator('[data-cache-refreshed]')).toHaveText(after!);
    write(session.root, file, changed.replace('TTL: time.Hour', 'TTL: 0'));
    await expect.poll(() => session.output, { timeout: 60000 }).toContain('TTL must be positive');
    write(session.root, file, changed);
    await expect.poll(async () => { try { return (await request.get(url)).status(); } catch { return 0; } }, { timeout: 60000 }).toBe(200);
    const uncached = changed.replace('return []loader.Binding{', '_ = options\n return []loader.Binding{')
      .replace('loader.BindCached("cache-demo/post/[id]/page.templ", loadCacheDemo, options)', 'loader.Bind("cache-demo/post/[id]/page.templ", loadCacheDemo)')
      .replace('loader.BindCached("cache-demo/team/[id]/page.svelte", loadCacheDemo, options)', 'loader.Bind("cache-demo/team/[id]/page.svelte", loadCacheDemo)');
    const runs = (session.output.match(/running\.\.\./g) ?? []).length;
    write(session.root, file, uncached);
    await expect.poll(() => (session.output.match(/running\.\.\./g) ?? []).length, { timeout: 60000 }).toBeGreaterThan(runs);
    await page.reload(); const a = await page.locator('[data-cache-refreshed]').textContent();
    await page.reload(); await expect(page.locator('[data-cache-refreshed]')).not.toHaveText(a!);
    await pause(3000); const builds = (session.output.match(/building\.\.\./g) ?? []).length; await pause(3000);
    expect((session.output.match(/building\.\.\./g) ?? []).length).toBe(builds);
  } finally {
    await session.stop(); expect(session.processesGone()).toBe(true); session.dispose();
  }
});
