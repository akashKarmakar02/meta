import { test, expect } from '@playwright/test';
import { spawn, execFileSync, type ChildProcess } from 'node:child_process';
import { mkdtempSync, cpSync, mkdirSync, symlinkSync, readFileSync, writeFileSync, rmSync, existsSync } from 'node:fs';
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
    const browserRequests: string[] = [];
    const sockets: string[] = [];
    page.on('request', (req) => { if (['script', 'stylesheet', 'fetch'].includes(req.resourceType())) browserRequests.push(req.url()); });
    page.on('websocket', (ws) => { sockets.push(ws.url()); });
    const dashboard = path.join(root, 'app/dashboard/page.svelte');
    const layout = path.join(root, 'app/dashboard/layout.svelte');
    const shared = path.join(root, 'app/dashboard/HmrShared.svelte');
    const pageOriginal = readFileSync(dashboard, 'utf8');
    const layoutOriginal = readFileSync(layout, 'utf8');
    writeFileSync(path.join(root, 'app/dashboard/hmr.css'), '[data-hmr-shared] { color: rgb(12, 34, 56); }');
    writeFileSync(shared, '<script>import "./hmr.css";</script><p data-hmr-shared>shared-original</p>');
    writeFileSync(dashboard, pageOriginal.replace('<script lang="ts">', '<script lang="ts">\n import HmrShared from "./HmrShared.svelte";').replace('</main>', '<HmrShared />\n</main>'));
    child = spawn(path.join(root, 'bin/meta'), ['dev'], { cwd: root, env: { ...fixtureEnv, PORT: String(backendPort), GO_META_VITE_PORT: String(vitePort) }, stdio: ['ignore', 'pipe', 'pipe'] });
    child.stdout?.on('data', (data) => { output += data.toString(); });
    child.stderr?.on('data', (data) => { output += data.toString(); });
    await expect.poll(async () => { try { return (await request.get(`${base}/healthz`)).status(); } catch { return 0; } }, { timeout: 60_000 }).toBe(200);
    await page.goto(`${base}/about`);
    await page.click('a[href="/dashboard"]');
    await expect(page.locator('[data-go-page="dashboard"]')).toBeVisible();
    await expect(page.locator('[data-hmr-shared]')).toHaveText('shared-original');
    await expect(page.locator('[data-hmr-shared]')).toHaveCSS('color', 'rgb(12, 34, 56)');
    await expect.poll(() => sockets.length, { timeout: 30_000 }).toBeGreaterThan(0);
    expect(sockets.every((url) => url.startsWith(`ws://127.0.0.1:${backendPort}/_go_meta_vite/`))).toBe(true);
    expect(browserRequests.some((url) => url.includes('/_go_meta_vite/@vite/client'))).toBe(true);
    expect(browserRequests.every((url) => new URL(url).origin === base)).toBe(true);
    expect(browserRequests.some((url) => new URL(url).port === String(vitePort))).toBe(false);
    const initialSockets = sockets.length;
    const entryModule = await request.get(`${base}/_go_meta_vite/frontend/entry.ts?test=a%2Fb`);
    expect(entryModule.status()).toBe(200);
    expect(await entryModule.text()).toContain('sourceMappingURL=');
    const cssModule = await request.get(`${base}/_go_meta_vite/app/dashboard/hmr.css?direct`);
    expect(cssModule.status()).toBe(200);
    expect(await cssModule.text()).toContain('rgb(12, 34, 56)');
    const viteChildren = (pid: number) => execFileSync('ps', ['-eo', 'pid=,ppid=,args='], { encoding: 'utf8' }).split('\n').flatMap((line) => {
      const match = line.trim().match(/^(\d+)\s+(\d+)\s+(.*)$/);
      return match && Number(match[2]) === pid && match[3].includes('/vite/bin/vite.js') ? [Number(match[1])] : [];
    });
    await expect.poll(() => viteChildren(child!.pid!).length).toBe(1);
    const vitePID = viteChildren(child.pid!)[0];
    const processList = execFileSync('ps', ['-eo', 'pid=,args='], { encoding: 'utf8' });
    const serverLine = processList.split('\n').find((line) => line.includes(`${root}/.tmp/air/server`));
    expect(serverLine).toBeTruthy();
    const serverPID = Number(serverLine!.trim().split(/\s+/)[0]);
    const socketEnv = readFileSync(`/proc/${serverPID}/environ`, 'utf8').split('\0').find((value) => value.startsWith('GO_META_DEV_SOCKET='));
    expect(socketEnv).toBeTruthy();
    const socketDir = path.dirname(socketEnv!.slice('GO_META_DEV_SOCKET='.length));
    expect(existsSync(socketDir)).toBe(true);
    for (const pathname of ['/unknown-application-route', '/_meta/image']) {
      const response = await request.get(base + pathname, { headers: { 'X-Go-Web-Navigation': '1' } });
      expect(response.status()).toBe(404);
      expect(response.headers()['content-type']).not.toContain('javascript');
    }
    await page.locator('[data-go-layout-counter-button]').click();
    await expect(page.locator('[data-go-layout-counter]')).toContainText('1');
    await page.evaluate(() => { (window as any).__hmrDocument = 'same'; (window as any).__hmrLayout = document.querySelector('[data-go-layout-shell]'); });
    await page.click('[data-go-dashboard-side-nav] a[href="/dashboard/settings"]');
    await expect(page.locator('[data-go-page="dashboard/settings"]')).toBeVisible();
    await page.click('[data-go-dashboard-side-nav] a[href="/dashboard"]');
    await expect(page.locator('[data-go-layout-counter]')).toContainText('1');
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
    // Restart Go while the public proxy and Vite WebSocket remain alive.
    const running = () => (output.match(/running\.\.\./g) ?? []).length;
    const previousRuns = running();
    const backend = path.join(root, 'cmd/example/main.go');
    const backendSource = readFileSync(backend, 'utf8');
    expect(backendSource).toContain('return c.SendString("ok")');
    writeFileSync(backend, backendSource.replace('return c.SendString("ok")', 'return c.SendString("ok-restarted:" + c.IP())'));
    await expect.poll(running, { timeout: 60_000 }).toBeGreaterThan(previousRuns);
    expect(viteChildren(child.pid!)).toEqual([vitePID]);
    expect(sockets).toHaveLength(initialSockets);
    await expect.poll(async () => { try { return await (await request.get(`${base}/healthz`, { timeout: 1000 })).text(); } catch { return ''; } }, { timeout: 60_000 }).toBe('ok-restarted:127.0.0.1');
    await page.click('[data-go-dashboard-side-nav] a[href="/dashboard/settings"]');
    await expect(page.locator('[data-go-page="dashboard/settings"]')).toBeVisible();
    await page.click('[data-go-dashboard-side-nav] a[href="/dashboard"]');
    await expect(page.locator('[data-go-page="dashboard"]')).toBeVisible();
    await expect(page.locator('[data-go-layout-counter]')).toContainText('1');
    writeFileSync(dashboard, readFileSync(dashboard, 'utf8').replace('Dashboard HMR updated', 'Dashboard after restart'));
    await expect(page.locator('h1')).toHaveText('Dashboard after restart', { timeout: 30_000 });
    expect(await page.evaluate(() => (window as any).__hmrDocument)).toBe('same');
    expect(sockets.every((url) => url.startsWith(`ws://127.0.0.1:${backendPort}/_go_meta_vite/`))).toBe(true);
    // Invalid Svelte must be reported by Vite and recover in the same session.
    writeFileSync(dashboard, '<script>const broken = ;</script>');
    // Request the changed module as a browser would; this also forces Vite to
    // surface the diagnostic if its filesystem events were coalesced.
    await expect.poll(async () => (await request.get(`${base}/_go_meta_vite/app/dashboard/page.svelte?import&invalid-probe=1`)).status(), { timeout: 30_000 }).toBe(500);
    await expect.poll(async () => output.includes('error') || output.includes('Error') || (await page.locator('vite-error-overlay').count()) > 0, { timeout: 30_000 }).toBe(true);
    await pause(1200); // Let the watcher settle before saving the fix.
    writeFileSync(dashboard, pageOriginal.replace('<h1>Dashboard</h1>', '<h1>Dashboard recovered</h1>'));
    await expect(page.locator('h1')).toHaveText('Dashboard recovered', { timeout: 30_000 });
    expect(viteChildren(child.pid!)).toEqual([vitePID]);
    // Vite may recover a failed compilation with a full reload. Mark the
    // resulting document for the subsequent ordinary layout HMR check.
    await page.evaluate(() => { (window as any).__hmrDocument = 'same'; });
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
    expect(browserRequests.every((url) => new URL(url).origin === base)).toBe(true);
    expect(sockets.every((url) => new URL(url).port === String(backendPort))).toBe(true);
    child.kill('SIGINT');
    const exit = await Promise.race([new Promise<number | null>((resolve) => child!.once('exit', resolve)), pause(20_000).then(() => { throw Error('meta dev did not stop'); })]);
    expect(exit).toBe(0);
    child = undefined;
    await expect.poll(async () => { try { await request.get(`${base}/healthz`, { timeout: 500 }); return true; } catch { return false; } }).toBe(false);
    await expect.poll(async () => { try { await request.get(`http://127.0.0.1:${vitePort}/@vite/client`, { timeout: 500 }); return true; } catch { return false; } }).toBe(false);
    const ps = execFileSync('ps', ['-eo', 'args'], { encoding: 'utf8' });
    expect(ps.includes(`${root}/.tmp/air/server`)).toBe(false);
    expect(ps.includes(`${root}/node_modules/vite/bin/vite.js`)).toBe(false);
    expect(existsSync(socketDir)).toBe(false);
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
    // An unexpected Vite exit must stop the public proxy and Air too.
    let crashOutput = '';
    const crash = spawn(path.join(root, 'bin/meta'), ['dev'], { cwd: root, env: { ...fixtureEnv, PORT: String(backendPort), GO_META_VITE_PORT: String(vitePort) } });
    crash.stdout.on('data', (data) => { crashOutput += data.toString(); });
    crash.stderr.on('data', (data) => { crashOutput += data.toString(); });
    try {
      await expect.poll(async () => { try { return (await request.get(`${base}/healthz`, { timeout: 1000 })).status(); } catch { return 0; } }, { timeout: 60_000 }).toBe(200);
      await expect.poll(() => viteChildren(crash.pid!).length, { timeout: 15_000 }).toBe(1);
      process.kill(viteChildren(crash.pid!)[0], 'SIGTERM');
      const status = await Promise.race([new Promise<number | null>((resolve) => crash.once('exit', resolve)), pause(20_000).then(() => { throw Error('Vite crash did not stop meta dev'); })]);
      expect(status).not.toBe(0);
      expect(crashOutput).toContain('Vite exited unexpectedly');
      await expect.poll(async () => { try { await request.get(`${base}/healthz`, { timeout: 500 }); return true; } catch { return false; } }).toBe(false);
      const remaining = execFileSync('ps', ['-eo', 'args'], { encoding: 'utf8' });
      expect(remaining.includes(`${root}/.tmp/air/server`)).toBe(false);
      expect(remaining.includes(`${root}/node_modules/vite/bin/vite.js`)).toBe(false);
    } finally {
      if (crash.exitCode === null) { crash.kill('SIGINT'); await Promise.race([new Promise((resolve) => crash.once('exit', resolve)), pause(10_000)]); if (crash.exitCode === null) crash.kill('SIGKILL'); }
    }
  } catch (err) {
    console.error(output);
    throw err;
  } finally {
    if (child && child.exitCode === null) { child.kill('SIGINT'); await Promise.race([new Promise((resolve) => child!.once('exit', resolve)), pause(10_000)]); if (child.exitCode === null) child.kill('SIGKILL'); }
    rmSync(root, { recursive: true, force: true });
  }
});
