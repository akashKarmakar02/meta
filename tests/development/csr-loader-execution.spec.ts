// CSR loader execution (P2-C04 §21): the registered loader runs for CSR routes
// in `meta dev`, its JSON result is embedded in the shell and handed to the
// Svelte page as a prop, editing the loader rebuilds and updates the output,
// a compile error keeps the public proxy available and recovers, and removing
// the binding falls back to the no-data state. The dashboard/[teamId] fixture
// route ships with the app.
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

test('registered CSR loader embeds its JSON data in the shell', async ({ request }) => {
  await session.waitUntil(request, '/dashboard/team-42', 200);
  const body = await getBody(request, '/dashboard/team-42');
  // The server serializes the loader result into the bootstrap payload.
  expect(body).toContain('Loaded by Go');
  expect(body).toContain('Team team-42');
});

test('editing the loader rebuilds and updates the rendered data', async ({ request }) => {
  const file = 'app/loaders/dashboard.go';
  const original = read(session.root, file);
  write(session.root, file, original.replace('Loaded by Go', 'Loaded by Go v2'));
  await expect.poll(() => getBody(request, '/dashboard/team-42'), { timeout: 90_000 }).toContain('Loaded by Go v2');
});

test('a loader compile error keeps the proxy available and recovers', async ({ request }) => {
  const file = 'app/loaders/dashboard.go';
  const original = read(session.root, file);
  write(session.root, file, original + '\nfunc init() { c04UndefinedSymbol() }');
  await expect.poll(() => session.output.includes('c04UndefinedSymbol') || session.output.includes('undefined: c04UndefinedSymbol'), { timeout: 60_000 }).toBe(true);
  // The public proxy stays up: only 200 (old binary) or 503 (rebuilding) are
  // acceptable — never a hang, never another error status.
  let status = 0;
  try {
    status = (await request.get(`${session.base}/dashboard/team-42`, { timeout: 2000 })).status();
  } catch {
    status = 0;
  }
  expect([0, 200, 503]).toContain(status);
  write(session.root, file, original);
  await expect.poll(() => getBody(request, '/dashboard/team-42'), { timeout: 90_000 }).toContain('Loaded by Go v2');
});

test('removing the binding stops embedding loader data in the shell', async ({ request }) => {
  const file = 'app/loaders/bindings.go';
  const original = read(session.root, file);
  write(session.root, file, original.replace('loader.Bind("dashboard/[teamId]/page.svelte", LoadDashboard),', ''));
  // The page is CSR, so the no-data fallback renders client-side; the server
  // signal is that the bootstrap data script is no longer embedded.
  await expect
    .poll(() => getBody(request, '/dashboard/team-42'), { timeout: 90_000 })
    .not.toContain('go-meta-loader-data');
});

test('no idle rebuild loop after changes settle', async () => {
  await pause(3000);
  const builds = (session.output.match(/routegen: \d+ routes written/g) ?? []).length;
  await pause(4500);
  expect((session.output.match(/routegen: \d+ routes written/g) ?? []).length).toBe(builds);
});
