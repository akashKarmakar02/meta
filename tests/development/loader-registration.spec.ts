import { test, expect } from '@playwright/test';
import { mkdirSync, renameSync, rmSync } from 'node:fs';
import path from 'node:path';
import { newDevSession, write, pause, type DevSession } from './fixtures/dev-session';

let session: DevSession;
const source = (ids: string[], marker: string) => `package loaders
import ("context"; "log"; "go-meta/loader"; "go-meta/routecontext")
func load(context.Context, routecontext.Route) (int, error) { return 1, nil }
func Bindings() []loader.Binding { log.Print("${marker}"); return []loader.Binding{${ids.map(id => `loader.Bind(${JSON.stringify(id)}, load),`).join('')}} }
`;
const file = 'app/loaders/bindings.go';
const oldID = 'c02route/[id]/page.svelte';
const newID = 'c02renamed/[id]/page.svelte';

test.beforeAll(async () => { session = await newDevSession(); await session.start(); });
test.afterAll(async () => { await session.stop(); session.dispose(); });

test('Go bindings validate, rebuild, and recover after route changes', async ({request}) => {
 await session.waitUntil(request, '/healthz', 200);
 mkdirSync(path.join(session.root,'app/c02route/[id]'), {recursive:true});
 write(session.root,'app/c02route/[id]/page.svelte','<h1>C02 route</h1>');
 await session.waitUntil(request, '/c02route/42', 200);
 write(session.root,file,source([oldID],'C02-binding-added'));
 await expect.poll(() => session.output.includes('C02-binding-added'),{timeout:90_000}).toBe(true);
 await session.waitUntil(request, '/c02route/42', 200);
 write(session.root,file,source([oldID],'C02-binding-edited'));
 await expect.poll(() => session.output.includes('C02-binding-edited'),{timeout:90_000}).toBe(true);
 write(session.root,file,source([oldID,oldID],'C02-duplicate'));
 await expect.poll(() => session.output.includes('duplicate binding'),{timeout:90_000}).toBe(true);
 write(session.root,file,source(['missing/page.svelte'],'C02-unknown'));
 await expect.poll(() => session.output.includes('unknown route ID'),{timeout:90_000}).toBe(true);
 write(session.root,file,source([oldID],'C02-recovered'));
 await expect.poll(() => session.output.includes('C02-recovered'),{timeout:90_000}).toBe(true);
 await session.waitUntil(request, '/c02route/42', 200);
 renameSync(path.join(session.root,'app/c02route'),path.join(session.root,'app/c02renamed'));
 await expect.poll(() => (session.output.match(/unknown route ID/g) ?? []).length,{timeout:90_000}).toBeGreaterThan(1);
 write(session.root,file,source([newID],'C02-renamed'));
 await expect.poll(() => session.output.includes('C02-renamed'),{timeout:90_000}).toBe(true);
 await session.waitUntil(request, '/c02renamed/42', 200);
 rmSync(path.join(session.root,'app/c02renamed'),{recursive:true,force:true});
 await expect.poll(() => (session.output.match(/unknown route ID/g) ?? []).length,{timeout:90_000}).toBeGreaterThan(2);
 write(session.root,file,source([],'C02-removed'));
 await expect.poll(() => session.output.includes('C02-removed'),{timeout:90_000}).toBe(true);
 await session.waitUntil(request, '/c02renamed/42', 404);
 await pause(3000); const builds=(session.output.match(/routegen: \d+ routes written/g)??[]).length;
 await pause(4500);expect((session.output.match(/routegen: \d+ routes written/g)??[]).length).toBe(builds);
 expect(session.output).not.toContain('C02-UNEXPECTED-EXECUTION');
});
