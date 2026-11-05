import { test, expect } from '@playwright/test';
import { newDevSession, read, write, pause } from './fixtures/dev-session';

test('image contract is proxied and Go edits rebuild without an idle loop', async ({ request }) => {
  const session = await newDevSession();
  try {
    await session.start();
    await session.waitUntil(request, '/_meta/image?src=%2Fimages%2Fhero.jpg&w=20', 501);
    const file = 'imageopt/imageopt.go';
    const builds = (session.output.match(/building\.\.\./g) ?? []).length;
    write(session.root, file, read(session.root, file) + '\n// Development rebuild fixture.\n');
    await expect.poll(() => (session.output.match(/building\.\.\./g) ?? []).length, { timeout: 60000 }).toBeGreaterThan(builds);
    await session.waitUntil(request, '/_meta/image?src=%2Fimages%2Fhero.jpg&w=20', 501);
    await pause(3000);
    const settled = (session.output.match(/building\.\.\./g) ?? []).length;
    await pause(3000);
    expect((session.output.match(/building\.\.\./g) ?? []).length).toBe(settled);
  } finally {
    await session.stop(); expect(session.processesGone()).toBe(true); session.dispose();
  }
});
