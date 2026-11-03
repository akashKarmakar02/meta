// B03 development acceptance (blueprint §7): catch-all route regeneration in
// `meta dev` — create, edit, rename, delete, malformed, and recovery, with no
// idle rebuild loop and no orphaned generated artifacts.
//
// The production route set is untouched; the session runs in an isolated repo
// copy (DevSession) so the real generated/ and frontend/ stay clean. A fresh
// directory (app/guide) is used so the test never collides with the production
// catch-all routes that are copied into the session. Each test resets app/guide
// first so the cases are independent. A server-rendered (templ) page is used so
// the rendered content is in the Go response, giving a reliable signal that an
// edit was picked up by the air rebuild.
import { test, expect } from '@playwright/test';
import { mkdirSync, rmSync, renameSync, writeFileSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { newDevSession, write, pause, type DevSession } from './fixtures/dev-session';

let session: DevSession;
const deep = '/guide/getting-started/installation';
const nav = { headers: { 'X-Go-Web-Navigation': '1' } };
const guideDir = () => path.join(session.root, 'app/guide');
const pageFile = 'app/guide/[...slug]/page.templ';

const templPage = (body: string) =>
  `package guide\n\ntempl Page() {\n\t<main data-go-page="catchall">${body}</main>\n}\n`;

// Reset app/guide so each case starts from a clean slate.
function resetGuide(): void {
  rmSync(guideDir(), { recursive: true, force: true });
}

test.beforeAll(async () => {
  session = await newDevSession();
  await session.start();
});

test.afterAll(async () => {
  await session.stop();
  session.dispose();
});

test('development creates, edits, renames, and deletes a catch-all route', async ({ request }) => {
  resetGuide();

  // Create the catch-all route; the dev server regenerates and serves it.
  mkdirSync(path.join(session.root, 'app/guide/[...slug]'), { recursive: true });
  write(session.root, pageFile, templPage('catch-all'));
  await session.waitUntil(request, deep, 200);
  const envelope = await (await request.get(`${session.base}${deep}`, nav)).json();
  expect(envelope).toMatchObject({
    version: 1,
    url: deep,
    routeId: 'guide/[...slug]/page.templ',
    mode: 'ssr',
  });
  // The concrete path is not a module path: the route id is the declared
  // source path (verified in the envelope above), and the SSR response renders
  // the page content.
  expect(await (await request.get(`${session.base}${deep}`)).text()).toContain('catch-all');

  // Edit the page; the air rebuild reflects the change.
  write(session.root, pageFile, templPage('catch-all v2'));
  await expect
    .poll(async () => {
      try {
        return (await (await request.get(`${session.base}${deep}`)).text()).includes('catch-all v2');
      } catch {
        return false;
      }
    }, { timeout: 90_000 })
    .toBe(true);

  // Rename the catch-all directory; the route id changes and the old one is
  // gone, with no orphaned generated entry for the old name.
  const renamed = path.join(session.root, 'app/guide/[...section]');
  renameSync(path.join(session.root, 'app/guide/[...slug]'), renamed);
  await expect
    .poll(async () => {
      try {
        const r = await request.get(`${session.base}${deep}`, nav);
        return (await r.json()).routeId;
      } catch {
        return '';
      }
    }, { timeout: 90_000 })
    .toBe('guide/[...section]/page.templ');
  const registry = readFileSync(path.join(session.root, 'frontend/route-registry.generated.ts'), 'utf8');
  expect(registry).toContain('guide/[...section]/page.templ');
  expect(registry).not.toContain('guide/[...slug]/page.templ');

  // Delete the route; the dev server answers 404 and stays alive.
  rmSync(renamed, { recursive: true, force: true });
  await session.waitUntil(request, deep, 404);
  expect((await request.get(`${session.base}/`)).status()).toBe(200);
});

test('development rejects a malformed catch-all and recovers', async ({ request }) => {
  resetGuide();

  // A valid catch-all is served.
  mkdirSync(path.join(session.root, 'app/guide/[...slug]'), { recursive: true });
  write(session.root, pageFile, templPage('ok'));
  await session.waitUntil(request, deep, 200);

  // A malformed catch-all (unbalanced) must be rejected by the scanner with
  // INVALID_SEGMENT and must not take the dev server down.
  const badDir = path.join(session.root, 'app/guide/[...slug');
  mkdirSync(badDir, { recursive: true });
  writeFileSync(path.join(badDir, 'page.templ'), templPage('broken'), 'utf8');
  await expect
    .poll(() => session.output.includes('INVALID_SEGMENT'), { timeout: 90_000 })
    .toBe(true);
  // The server is still alive (any non-crash response).
  const alive = await request.get(`${session.base}/`);
  expect(alive.status()).toBeLessThan(500);

  // Recovery: remove the malformed directory and restore a clean valid route.
  rmSync(badDir, { recursive: true, force: true });
  await session.waitUntil(request, deep, 200);
});

test('no idle rebuild loop with a stable catch-all route', async ({ request }) => {
  resetGuide();
  mkdirSync(path.join(session.root, 'app/guide/[...slug]'), { recursive: true });
  write(session.root, pageFile, templPage('stable'));
  await session.waitUntil(request, deep, 200);
  await pause(3000);
  const builds = (session.output.match(/routegen: \d+ routes written/g) ?? []).length;
  await pause(4500);
  // No new generation pass while idle: the dev server must not spin a loop.
  expect((session.output.match(/routegen: \d+ routes written/g) ?? []).length).toBe(builds);
  // Still serving the route.
  expect((await request.get(`${session.base}${deep}`)).status()).toBe(200);
});
