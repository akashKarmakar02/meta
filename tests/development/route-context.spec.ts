import { test, expect } from '@playwright/test';
import { mkdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { newDevSession, write, pause, type DevSession } from './fixtures/dev-session';

let session: DevSession;
const pageFile = 'app/context-users/[userId]/posts/[postId]/page.templ';
const ssr = '/context-users/42/posts/7?tab=comments&tag=go&tag=web';
const csr = '/contextclient/42?q=hello';
const catchall = '/context-docs/42/getting-started/install?lang=en';
const page = (heading: string) => `package contextpage
import (
 "fmt"
 "strings"
 "go-meta/routecontext"
 "go-meta/routeparams"
)
templ Page() {
 {{
   r, _ := routecontext.FromContext(ctx)
   user, _ := r.Params.Int64("userId")
   post, _ := routeparams.FromContext(ctx).Int64("postId")
 }}
 <main><h1>${heading}</h1><p data-route>{r.RouteID}</p><p data-pattern>{r.Pattern}</p><p data-user>{fmt.Sprint(user)}</p><p data-post>{fmt.Sprint(post)}</p><p data-tab>{r.Query.Get("tab")}</p><p data-tags>{strings.Join(r.Query.Values("tag"), ",")}</p></main>
}\n`;

test.beforeAll(async () => {
 session = await newDevSession();
 await session.start();
 for (const dir of ['app/context-users/[userId]/posts/[postId]', 'app/context-docs/[id]/[...slug]', 'app/contextclient/[id]']) mkdirSync(path.join(session.root, dir), { recursive: true });
 write(session.root, pageFile, page('Route context'));
 write(session.root, 'app/context-docs/[id]/[...slug]/page.templ', `package contextdocs
import "go-meta/routecontext"
templ Page() {
 {{ r, _ := routecontext.FromContext(ctx); slug, _ := r.Params.Get("slug"); id, _ := r.Params.Get("id") }}
 <main><h1>Context docs</h1><p>{r.Pattern}</p><p>{id}</p><p>{slug}</p><p>{r.Query.Get("lang")}</p></main>
}\n`);
 write(session.root, 'app/contextclient/layout.templ', `package contextclient
import "go-meta/routecontext"
templ ContextclientLayout(c templ.Component) {
 {{ r, _ := routecontext.FromContext(ctx) }}
 <div data-client-route={r.RouteID} data-client-query={r.Query.Get("q")}>@c</div>
}\n`);
 write(session.root, 'app/contextclient/[id]/page.svelte', '<h1>Context client</h1>');
});

test('CSR shell and envelope receive context in their templ layout', async ({ request }) => {
 await session.waitUntil(request, csr, 200);
 const html = await (await request.get(`${session.base}${csr}`)).text();
 expect(html).toContain('contextclient/[id]/page.svelte');
 expect(html).toContain('data-client-query="hello"');
 const nav = await (await request.get(`${session.base}${csr}`, { headers: { 'X-Go-Web-Navigation': '1' } })).json();
 expect(nav.version).toBe(1);
 expect(nav.html).toContain('data-client-query="hello"');
 expect(nav.routeId).toBe('contextclient/[id]/page.svelte');
});

test('edit, failed compile and recovery retain context without rebuild loops', async ({ request }) => {
 write(session.root, pageFile, page('Edited context'));
 await expect.poll(async () => { try { return (await (await request.get(`${session.base}${ssr}`)).text()).includes('Edited context'); } catch { return false; } }, {timeout: 90_000}).toBe(true);
 const valid = readFileSync(path.join(session.root, pageFile), 'utf8');
 write(session.root, pageFile, valid.replace('r.Query.Get("tab")', 'missingSymbol'));
 await expect.poll(() => session.output.includes('undefined: missingSymbol'), {timeout: 90_000}).toBe(true);
 write(session.root, pageFile, page('Recovered context'));
 await expect.poll(async () => { try { return (await (await request.get(`${session.base}${ssr}`)).text()).includes('Recovered context'); } catch { return false; } }, {timeout: 90_000}).toBe(true);
 await pause(3000);
 const builds = (session.output.match(/routegen: \d+ routes written/g) ?? []).length;
 await pause(4500);
 expect((session.output.match(/routegen: \d+ routes written/g) ?? []).length).toBe(builds);
});
test.afterAll(async () => { await session.stop(); session.dispose(); });

test('SSR context and catch-all render in direct and navigation requests', async ({ request }) => {
 await session.waitUntil(request, ssr, 200);
 let response = await request.get(`${session.base}${ssr}`);
 let html = await response.text();
 for (const expected of ['context-users/[userId]/posts/[postId]/page.templ', '/context-users/[userId]/posts/[postId]', 'comments', 'go,web', '>42<', '>7<']) expect(html).toContain(expected);
 const nav = await (await request.get(`${session.base}${ssr}`, { headers: { 'X-Go-Web-Navigation': '1' } })).json();
 expect(nav.version).toBe(1); expect(nav.html).toContain('go,web');
 await session.waitUntil(request, catchall, 200);
 html = await (await request.get(`${session.base}${catchall}`)).text();
 for (const expected of ['/context-docs/[id]/[...slug]', 'getting-started/install', '>42<', '>en<']) expect(html).toContain(expected);
});
