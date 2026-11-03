import { test, expect } from '@playwright/test';
import { mkdirSync, renameSync, rmSync } from 'node:fs';
import path from 'node:path';
import { newDevSession, write, pause, type DevSession } from './fixtures/dev-session';

let session: DevSession;
const nav = { headers: { 'X-Go-Web-Navigation': '1' } };
const templ = (label: string, name = "Page") => `package precedence\ntempl ${name}() { <main><h1>${label}</h1><a href="/b06/42">Dynamic link</a><a href="/b06/a/b">Catch-all link</a></main> }\n`;

test.beforeAll(async () => {
 session = await newDevSession();
 await session.start();
 for (const dir of ['app/b06/new', 'app/b06/[id]', 'app/b06/[...tail]']) mkdirSync(path.join(session.root, dir), {recursive:true});
 write(session.root, 'app/b06/new/page.templ', templ('Static winner', 'B06New'));
 write(session.root, 'app/b06/[id]/layout.svelte', `<script lang="ts">import type { Snippet } from 'svelte'; let { children }: { children?: Snippet } = $props(); let count = $state(0);</script><div data-go-layout-shell data-go-layout="app/b06/[id]/layout.svelte"><button data-count onclick={() => count++}>Count {count}</button><div data-go-layout-outlet>{@render children?.()}</div></div>`);
 write(session.root, 'app/b06/[id]/page.svelte', '<h1>Dynamic winner</h1><a href="/b06/new">Static link</a><a href="/b06/other">Other value</a>');
 write(session.root, 'app/b06/[...tail]/page.templ', templ('Catch-all winner'));
});

test('overlap selection, conflict recovery, and rename', async ({request, page}) => {
 await session.waitUntil(request, '/b06/new', 200);
 await expect.poll(async () => { try {return (await (await request.get(`${session.base}/b06/42`, nav)).json()).routeId;} catch {return '';} }, {timeout:90_000}).toBe('b06/[id]/page.svelte');
 const identity = async (url:string) => (await (await request.get(`${session.base}${url}`,nav)).json()).routeId;
 expect(await identity('/b06/new')).toBe('b06/new/page.templ');
 expect(await identity('/b06/a/b')).toBe('b06/[...tail]/page.templ');
 await page.goto(`${session.base}/b06/42`);
 await expect(page.getByRole('heading',{name:'Dynamic winner'})).toBeVisible();
 await page.reload();
 await expect(page.getByRole('heading',{name:'Dynamic winner'})).toBeVisible();
 await page.evaluate(() => { (window as any).__b06 = 1; });
 await page.locator('[data-count]').click();
 await page.getByText('Other value').click();
 await expect(page).toHaveURL(`${session.base}/b06/other`);
 await expect(page.locator('[data-count]')).toHaveText('Count 1');
 await page.getByText('Static link').click();
 await expect(page.getByRole('heading',{name:'Static winner'})).toBeVisible();
 expect(await page.evaluate(() => (window as any).__b06)).toBe(1);
 await page.goBack();
 await expect(page.getByRole('heading',{name:'Dynamic winner'})).toBeVisible();
 await page.goForward();
 await page.getByText('Catch-all link').click();
 await expect(page.getByRole('heading',{name:'Catch-all winner'})).toBeVisible();
 await page.getByText('Dynamic link').click();
 await expect(page.getByRole('heading',{name:'Dynamic winner'})).toBeVisible();
 expect(await page.evaluate(() => (window as any).__b06)).toBe(1);
 write(session.root, 'app/b06/new/page.templ', templ('Edited static', 'B06New'));
 await expect.poll(async () => { try {return (await (await request.get(`${session.base}/b06/new`)).text()).includes('Edited static');} catch {return false;} }, {timeout:90_000}).toBe(true);
 mkdirSync(path.join(session.root,'app/b06/[name]'),{recursive:true});
 write(session.root,'app/b06/[name]/page.svelte','<h1>Conflict</h1>');
 await expect.poll(() => session.output.includes('ambiguous dynamic segments'),{timeout:90_000}).toBe(true);
 expect(await identity('/b06/42')).toBe('b06/[id]/page.svelte');
 rmSync(path.join(session.root,'app/b06/[name]'),{recursive:true,force:true});
 renameSync(path.join(session.root,'app/b06/new'),path.join(session.root,'app/b06/special'));
 write(session.root, 'app/b06/special/page.templ', templ('Edited static', 'B06Special'));
 await expect.poll(async () => {try {return await identity('/b06/special');}catch{return '';}},{timeout:90_000}).toBe('b06/special/page.templ');
 expect(await identity('/b06/new')).toBe('b06/[id]/page.svelte');
 await pause(3000);
 const builds=(session.output.match(/routegen: \d+ routes written/g)??[]).length;
 await pause(4500);
 expect((session.output.match(/routegen: \d+ routes written/g)??[]).length).toBe(builds);
});

test.afterAll(async () => {await session.stop(); session.dispose();});
