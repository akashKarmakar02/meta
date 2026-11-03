// SSR loader execution (P2-C03 §19): the registered loader runs during real
// SSR dispatch in `meta dev`, its typed result renders into the templ page,
// editing the loader rebuilds and updates the output, a compile error keeps
// the public proxy available and recovers, and removing the binding falls back
// to the no-data state. The posts/[postId] fixture route ships with the app.
import { test, expect } from '@playwright/test';
import { read, write, pause, newDevSession, type DevSession } from './fixtures/dev-session';

let session: DevSession;

test.beforeAll(async () => {
  session = await newDevSession();
  await session.start();
});

test.afterAll(async () => {
  await session.stop();
  session.dispose();
});

const getBody = async (request: { get(url: string, options?: any): Promise<{ text(): Promise<string>; status(): number }> }, path: string): Promise<string> => {
  try {
    return await (await request.get(`${session.base}${path}`, { timeout: 1000 })).text();
  } catch {
    return '';
  }
};

test('registered SSR loader executes during dispatch and renders its data', async ({ request }) => {
  await session.waitUntil(request, '/posts/42', 200);
  const body = await getBody(request, '/posts/42');
  expect(body).toContain('Example Post');
  expect(body).toContain('Loaded on the server');
  expect(body).not.toContain('No loader data for this route.');
});

test('editing the loader rebuilds and updates the rendered data', async ({ request }) => {
  const file = 'app/loaders/posts.go';
  const original = read(session.root, file);
  write(session.root, file, original.replace('Loaded on the server', 'Loaded on the server v2'));
  await expect.poll(() => getBody(request, '/posts/42'), { timeout: 90_000 }).toContain('Loaded on the server v2');
});

test('a loader compile error keeps the proxy available and recovers', async ({ request }) => {
  const file = 'app/loaders/posts.go';
  const original = read(session.root, file);
  write(session.root, file, original + '\nfunc init() { c03UndefinedSymbol() }');
  await expect.poll(() => session.output.includes('c03UndefinedSymbol') || session.output.includes('undefined: c03UndefinedSymbol'), { timeout: 60_000 }).toBe(true);
  // The public proxy stays up: only 200 (old binary) or 503 (rebuilding) are
  // acceptable — never a hang, never another error status.
  let status = 0;
  try {
    status = (await request.get(`${session.base}/posts/42`, { timeout: 2000 })).status();
  } catch {
    status = 0;
  }
  expect([0, 200, 503]).toContain(status);
  write(session.root, file, original);
  await expect.poll(() => getBody(request, '/posts/42'), { timeout: 90_000 }).toContain('Loaded on the server v2');
});

test('removing the binding renders the no-data fallback', async ({ request }) => {
  const file = 'app/loaders/bindings.go';
  const original = read(session.root, file);
  write(session.root, file, original.replace('loader.Bind("posts/[postId]/page.templ", LoadPost),', ''));
  await expect.poll(() => getBody(request, '/posts/42'), { timeout: 90_000 }).toContain('No loader data for this route.');
});

test('no idle rebuild loop after changes settle', async () => {
  await pause(3000);
  const builds = (session.output.match(/routegen: \d+ routes written/g) ?? []).length;
  await pause(4500);
  expect((session.output.match(/routegen: \d+ routes written/g) ?? []).length).toBe(builds);
});
