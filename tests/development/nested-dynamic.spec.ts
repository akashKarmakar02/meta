import { test, expect } from '@playwright/test';
import { mkdirSync, rmSync, renameSync, readFileSync, existsSync } from 'node:fs';
import path from 'node:path';
import { newDevSession, write, pause, type DevSession } from './fixtures/dev-session';

let session: DevSession;
const ssr = '/users/42/posts/99';
const csr = '/teams/engineering/members/alice';
const other = '/teams/sales/members/bob';

test.beforeAll(async () => {
  session = await newDevSession();
  await session.start();
  for (const dir of ['app/users/[userId]/posts/[postId]', 'app/teams/[teamId]/members/[memberId]']) {
    mkdirSync(path.join(session.root, dir), { recursive: true });
  }
  write(session.root, 'app/users/layout.templ', 'package users\ntempl UsersLayout(c templ.Component) { <div data-users-layout>@c</div> }\n');
  write(session.root, 'app/users/[userId]/posts/[postId]/page.templ', `package userspage\ntempl Page() { <main><h1>Nested SSR</h1><a href="${csr}">to CSR</a></main> }\n`);
  write(session.root, 'app/teams/layout.svelte', `<script lang="ts">import type { Snippet } from 'svelte'; let { children }: { children?: Snippet } = $props(); let count = $state(0);</script><div data-go-layout-shell data-go-layout="app/teams/layout.svelte"><button data-counter onclick={() => count++}>Count {count}</button><div data-go-layout-outlet>{@render children?.()}</div></div>`);
  write(session.root, 'app/teams/[teamId]/members/[memberId]/page.svelte', `<main><h1>Nested CSR</h1><a href="${ssr}">to SSR</a><a href="${other}">other member</a><a href="${csr}">alice</a></main>`);
});

test.afterAll(async () => { await session.stop(); session.dispose(); });

test('nested SSR and CSR envelopes, browser history and layout ownership', async ({ page, request }) => {
  await session.waitUntil(request, ssr, 200);
  await session.waitUntil(request, csr, 200);
  const nav = { headers: { 'X-Go-Web-Navigation': '1' } };
  const a = await (await request.get(`${session.base}${ssr}`, nav)).json();
  const b = await (await request.get(`${session.base}/users/100/posts/200`, nav)).json();
  expect(a).toMatchObject({ version: 1, url: ssr, routeId: 'users/[userId]/posts/[postId]/page.templ', mode: 'ssr', layouts: ['app/users/layout.templ'] });
  expect(b.routeId).toBe(a.routeId);
  expect(a.html).toContain('Nested SSR');
  const c = await (await request.get(`${session.base}${csr}`, nav)).json();
  expect(c).toMatchObject({ version: 1, url: csr, routeId: 'teams/[teamId]/members/[memberId]/page.svelte', clientEntry: 'teams/[teamId]/members/[memberId]/page.svelte', mode: 'csr', layouts: ['app/teams/layout.svelte'] });
  expect(await (await request.get(`${session.base}${csr}`)).text()).toContain(`data-go-route="${csr}"`);
  expect((await request.get(`${session.base}/users/42/posts/99/extra`)).status()).toBe(404);
  expect((await request.get(`${session.base}/users/x%2Fy/posts/99`)).status()).toBe(400);
  await page.goto(`${session.base}${csr}`);
  await expect(page.locator('h1')).toHaveText('Nested CSR');
  await page.reload();
  await expect(page.locator('h1')).toHaveText('Nested CSR');
  await page.goto(`${session.base}${ssr}`);
  await expect(page.locator('[data-users-layout]')).toBeVisible();
  await page.evaluate(() => { (window as any).__b02 = 1; });
  await page.getByText('to CSR').click();
  await expect(page.locator('h1')).toHaveText('Nested CSR');
  expect(await page.evaluate(() => (window as any).__b02)).toBe(1);
  await page.locator('[data-counter]').click();
  await page.getByText('other member').click();
  await expect(page).toHaveURL(`${session.base}${other}`);
  await expect(page.locator('[data-counter]')).toHaveText('Count 1');
  await page.goBack();
  await expect(page).toHaveURL(`${session.base}${csr}`);
  await expect(page.locator('[data-counter]')).toHaveText('Count 1');
  await page.goForward();
  await expect(page).toHaveURL(`${session.base}${other}`);
  let held: import('@playwright/test').Route | null = null;
  await page.route(`**${csr}`, route => {
    if (route.request().headers()['x-go-web-navigation']) { held = route; return; }
    return route.continue();
  });
  await page.getByText('alice').click();
  await expect.poll(() => held !== null).toBe(true);
  await page.getByText('to SSR').click();
  await expect(page.locator('h1')).toHaveText('Nested SSR');
  if (held) await (held as import('@playwright/test').Route).continue().catch(() => {});
  await page.unroute(`**${csr}`);
  await expect(page).toHaveURL(`${session.base}${ssr}`);
  await page.getByText('to CSR').click();
  await expect(page.locator('[data-counter]')).toHaveText('Count 0');
  await page.evaluate(() => {
    const link = document.createElement('a');
    link.href = '/users/42/posts/99/missing';
    link.textContent = 'missing nested route';
    document.body.append(link);
  });
  await page.getByText('missing nested route').click();
  await expect(page.locator('[data-go-error="404"]')).toBeVisible();
});

test('development regenerates dynamic packages, reports errors, and recovers', async ({ request }) => {
  const dir = 'app/users/[userId]/posts/[postId]';
  const pageFile = `${dir}/page.templ`;
  write(session.root, pageFile, `package userspage\ntempl Page() { <main><h1>Updated SSR</h1><a href="${csr}">to CSR</a></main> }\n`);
  await expect.poll(async () => { try { return (await (await request.get(`${session.base}${ssr}`)).text()).includes('Updated SSR'); } catch { return false; } }, { timeout: 60_000 }).toBe(true);
  const previous = readFileSync(path.join(session.root, pageFile), 'utf8');
  write(session.root, pageFile, previous + '\ntempl Broken( {{{');
  await expect.poll(() => session.output.includes('compile dynamic templ packages'), { timeout: 60_000 }).toBe(true);
  await session.waitUntil(request, ssr, 200);
  write(session.root, pageFile, previous.replace('Updated SSR', 'Recovered SSR'));
  await expect.poll(async () => { try { return (await (await request.get(`${session.base}${ssr}`)).text()).includes('Recovered SSR'); } catch { return false; } }, { timeout: 60_000 }).toBe(true);
  const renamed = path.join(session.root, 'app/users/[userId]/posts/[articleId]');
  renameSync(path.join(session.root, dir), renamed);
  await expect.poll(async () => { try { const r = await request.get(`${session.base}${ssr}`, { headers: { 'X-Go-Web-Navigation': '1' } }); return (await r.json()).routeId; } catch { return ''; } }, { timeout: 60_000 }).toContain('[articleId]');
  const registry = readFileSync(path.join(session.root, 'frontend/route-registry.generated.ts'), 'utf8');
  expect(registry).toContain('teams/[teamId]/members/[memberId]/page.svelte');
  expect(existsSync(path.join(renamed, 'page_templ.go'))).toBe(false);
  rmSync(path.join(session.root, 'app/users'), { recursive: true, force: true });
  await session.waitUntil(request, ssr, 404);
});

test('development HMR, dynamic rename, invalid declaration and recovery', async ({ page, request }) => {
  const file = 'app/teams/[teamId]/members/[memberId]/page.svelte';
  await session.waitUntil(request, csr, 200);
  await page.goto(`${session.base}${csr}`);
  await expect(page.locator('h1')).toHaveText('Nested CSR');
  const source = readFileSync(path.join(session.root, file), 'utf8');
  write(session.root, file, source.replace('Nested CSR', 'Updated CSR'));
  await expect(page.locator('h1')).toHaveText('Updated CSR', { timeout: 60_000 });
  renameSync(path.join(session.root, 'app/teams/[teamId]'), path.join(session.root, 'app/teams/[groupId]'));
  await expect.poll(async () => { try { const res = await request.get(`${session.base}${csr}`, { headers: { 'X-Go-Web-Navigation': '1' } }); return (await res.json()).routeId; } catch { return ''; } }, { timeout: 60_000 }).toContain('[groupId]');
  rmSync(path.join(session.root, 'app/teams/[groupId]'), { recursive: true, force: true });
  await session.waitUntil(request, csr, 404);
  mkdirSync(path.join(session.root, 'app/teams/[broken'), { recursive: true });
  write(session.root, 'app/teams/[broken/page.svelte', '<h1>Broken</h1>');
  await expect.poll(() => session.output.includes('INVALID_SEGMENT'), { timeout: 60_000 }).toBe(true);
  rmSync(path.join(session.root, 'app/teams/[broken'), { recursive: true, force: true });
  mkdirSync(path.join(session.root, 'app/teams/[fixed]'), { recursive: true });
  write(session.root, 'app/teams/[fixed]/page.svelte', '<h1>Recovered</h1>');
  await session.waitUntil(request, '/teams/recovered', 200);
  await pause(3000);
  const builds = (session.output.match(/routegen: \d+ routes written/g) ?? []).length;
  await pause(4500);
  expect((session.output.match(/routegen: \d+ routes written/g) ?? []).length).toBe(builds);
});
