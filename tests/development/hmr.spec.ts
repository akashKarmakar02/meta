import { test, expect } from '@playwright/test';
import { spawn, execFileSync, type ChildProcess } from 'node:child_process';
import { mkdtempSync, cpSync, mkdirSync, symlinkSync, readFileSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import net from 'node:net';

async function freePort(): Promise<number> {
  const server = net.createServer();
  await new Promise<void>((resolve, reject) => server.once('error', reject).listen(0, '127.0.0.1', resolve));
  const address = server.address();
  if (!address || typeof address === 'string') throw Error('no port');
  const port = address.port;
  await new Promise<void>((resolve, reject) => server.close((err) => err ? reject(err) : resolve()));
  return port;
}

const pause = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

test('Vite HMR, route generation, and shutdown in an isolated project', async ({ page, request }) => {
  const source = process.cwd();
  const root = mkdtempSync(path.join(tmpdir(), 'go-meta-hmr-'));
  let child: ChildProcess | undefined;
  let output = '';
  try {
    for (const name of ['.air.toml', 'go.mod', 'go.sum', 'package.json', 'vite.config.ts', 'tsconfig.json', 'app', 'cmd', 'internal', 'frontend', 'generated']) {
      cpSync(path.join(source, name), path.join(root, name), { recursive: true });
    }
    symlinkSync(path.join(source, 'node_modules'), path.join(root, 'node_modules'), 'dir');
    mkdirSync(path.join(root, 'bin'));
    const fixtureEnv = { ...process.env, GOFLAGS: '-buildvcs=false' };
    execFileSync('go', ['build', '-o', 'bin/meta', './cmd/meta'], { cwd: root, env: fixtureEnv, stdio: 'pipe' });
    const backendPort = await freePort();
    const vitePort = await freePort();
    const base = `http://127.0.0.1:${backendPort}`;
    const dashboard = path.join(root, 'app/dashboard/page.svelte');
    const layout = path.join(root, 'app/dashboard/layout.svelte');
    const shared = path.join(root, 'app/dashboard/HmrShared.svelte');
    const pageOriginal = readFileSync(dashboard, 'utf8');
    const layoutOriginal = readFileSync(layout, 'utf8');
    writeFileSync(shared, '<p data-hmr-shared>shared-original</p>');
    writeFileSync(dashboard, pageOriginal.replace('<script lang="ts">', '<script lang="ts">\n import HmrShared from "./HmrShared.svelte";').replace('</main>', '<HmrShared />\n</main>'));
    child = spawn(path.join(root, 'bin/meta'), ['dev'], { cwd: root, env: { ...fixtureEnv, PORT: String(backendPort), GO_META_VITE_PORT: String(vitePort) }, stdio: ['ignore', 'pipe', 'pipe'] });
    child.stdout?.on('data', (data) => { output += data.toString(); });
    child.stderr?.on('data', (data) => { output += data.toString(); });
    await expect.poll(async () => { try { return (await request.get(`${base}/healthz`)).status(); } catch { return 0; } }, { timeout: 60_000 }).toBe(200);
    await page.goto(`${base}/about`);
    await page.click('a[href="/dashboard"]');
    await expect(page.locator('[data-go-page="dashboard"]')).toBeVisible();
    await expect(page.locator('[data-hmr-shared]')).toHaveText('shared-original');
    await page.locator('[data-go-layout-counter-button]').click();
    await expect(page.locator('[data-go-layout-counter]')).toContainText('1');
    await page.evaluate(() => { (window as any).__hmrDocument = 'same'; (window as any).__hmrLayout = document.querySelector('[data-go-layout-shell]'); });
    const builds = () => (output.match(/building\.\.\./g) ?? []).length;
    await pause(3000);
    const buildsBefore = builds();
    writeFileSync(dashboard, readFileSync(dashboard, 'utf8').replace('<h1>Dashboard</h1>', '<h1>Dashboard HMR updated</h1>'));
    await expect(page.locator('h1')).toHaveText('Dashboard HMR updated', { timeout: 30_000 });
    await expect(page.locator('[data-go-layout-counter]')).toContainText('1');
    expect(await page.evaluate(() => (window as any).__hmrDocument)).toBe('same');
    expect(await page.evaluate(() => (window as any).__hmrLayout === document.querySelector('[data-go-layout-shell]'))).toBe(true);
    writeFileSync(shared, '<p data-hmr-shared>shared-updated</p>');
    await expect(page.locator('[data-hmr-shared]')).toHaveText('shared-updated', { timeout: 30_000 });
    await pause(3000);
    expect(builds()).toBe(buildsBefore);
    // Invalid Svelte must be reported by Vite and recover in the same session.
    writeFileSync(dashboard, '<script>const broken = ;</script>');
    await expect.poll(async () => output.includes('error') || output.includes('Error') || (await page.locator('vite-error-overlay').count()) > 0, { timeout: 30_000 }).toBe(true);
    writeFileSync(dashboard, pageOriginal.replace('<h1>Dashboard</h1>', '<h1>Dashboard recovered</h1>'));
    await expect(page.locator('h1')).toHaveText('Dashboard recovered', { timeout: 30_000 });
    expect(await page.evaluate(() => (window as any).__hmrDocument)).toBe('same');
    // Exercise Svelte layout HMR as its own boundary.
    writeFileSync(layout, layoutOriginal.replace('<h2>Dashboard</h2>', '<h2>Dashboard layout HMR</h2>'));
    await expect(page.locator('[data-go-dashboard-sidebar] h2')).toHaveText('Dashboard layout HMR', { timeout: 30_000 });
    await expect(page.locator('[data-go-page="dashboard"]')).toBeVisible();
    expect(await page.evaluate(() => (window as any).__hmrDocument)).toBe('same');
    await page.click('[data-go-dashboard-side-nav] a[href="/dashboard/settings"]');
    await expect(page.locator('[data-go-page="dashboard/settings"]')).toBeVisible();
    const routeDir = path.join(root, 'app/hmrroute');
    mkdirSync(routeDir);
    writeFileSync(path.join(routeDir, 'page.svelte'), '<h1>New HMR route</h1>');
    await expect.poll(async () => { try { return (await request.get(`${base}/hmrroute`)).status(); } catch { return 0; } }, { timeout: 60_000 }).toBe(200);
    await page.goto(`${base}/hmrroute`);
    await expect(page.locator('h1')).toHaveText('New HMR route', { timeout: 30_000 });
    rmSync(routeDir, { recursive: true });
    await expect.poll(async () => { try { return (await request.get(`${base}/hmrroute`)).status(); } catch { return 0; } }, { timeout: 60_000 }).toBe(404);
    child.kill('SIGINT');
    const exit = await Promise.race([new Promise<number | null>((resolve) => child!.once('exit', resolve)), pause(20_000).then(() => { throw Error('meta dev did not stop'); })]);
    expect(exit).toBe(0);
    child = undefined;
    await expect.poll(async () => { try { await request.get(`${base}/healthz`, { timeout: 500 }); return true; } catch { return false; } }).toBe(false);
    await expect.poll(async () => { try { await request.get(`http://127.0.0.1:${vitePort}/@vite/client`, { timeout: 500 }); return true; } catch { return false; } }).toBe(false);
    const ps = execFileSync('ps', ['-eo', 'args'], { encoding: 'utf8' });
    expect(ps.includes(`${root}/.tmp/air/server`)).toBe(false);
    expect(ps.includes(`${root}/node_modules/vite/bin/vite.js`)).toBe(false);
    // Vite's strict port reports the conflict and the supervisor stops Air.
    const occupied = net.createServer((socket) => socket.destroy());
    await new Promise<void>((resolve, reject) => occupied.once('error', reject).listen(vitePort, '127.0.0.1', resolve));
    let conflict = '';
    const blocked = spawn(path.join(root, 'bin/meta'), ['dev'], { cwd: root, env: { ...fixtureEnv, PORT: String(backendPort), GO_META_VITE_PORT: String(vitePort) } });
    blocked.stdout.on('data', (data) => { conflict += data.toString(); });
    blocked.stderr.on('data', (data) => { conflict += data.toString(); });
    try {
      const status = await Promise.race([new Promise<number | null>((resolve) => blocked.once('exit', resolve)), pause(20_000).then(() => { throw Error('port conflict did not stop meta dev'); })]);
      expect(status).not.toBe(0);
      expect(conflict).toMatch(/port.*in use/i);
      await expect.poll(async () => { try { await request.get(`${base}/healthz`, { timeout: 500 }); return true; } catch { return false; } }).toBe(false);
    } finally {
      if (blocked.exitCode === null) blocked.kill('SIGKILL');
      await new Promise<void>((resolve) => occupied.close(() => resolve()));
    }
  } catch (err) {
    console.error(output);
    throw err;
  } finally {
    if (child && child.exitCode === null) { child.kill('SIGINT'); await Promise.race([new Promise((resolve) => child!.once('exit', resolve)), pause(10_000)]); if (child.exitCode === null) child.kill('SIGKILL'); }
    rmSync(root, { recursive: true, force: true });
  }
});
