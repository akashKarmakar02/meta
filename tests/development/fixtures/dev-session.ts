// Shared development-session fixture for the focused Chromium specs
// (P2-A06 §13): each spec spins its own isolated copy of this repository,
// builds the real meta binary inside it, starts `meta dev` on reserved
// ports, and tears everything down — including orphan-process sweeps —
// even when an assertion fails.
import { spawn, execFileSync, type ChildProcess } from 'node:child_process';
import { mkdtempSync, cpSync, mkdirSync, symlinkSync, readFileSync, writeFileSync, rmSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import net from 'node:net';

export const pause = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

/** Reserve an unused loopback port (classic reserve-read-release). */
export async function freePort(): Promise<number> {
  const server = net.createServer();
  await new Promise<void>((resolve, reject) => server.once('error', reject).listen(0, '127.0.0.1', resolve));
  const address = server.address();
  if (!address || typeof address === 'string') throw Error('no port');
  const port = address.port;
  await new Promise<void>((resolve, reject) => server.close((err) => err ? reject(err) : resolve()));
  return port;
}

/** One development session against an isolated copy of the repo. */
export class DevSession {
  readonly root: string;
  readonly base: string;
  readonly vitePort: number;
  child?: ChildProcess;
  output = '';

  constructor(root: string, publicPort: number, vitePort: number) {
    this.root = root;
    this.base = `http://127.0.0.1:${publicPort}`;
    this.vitePort = vitePort;
  }

  /** Start meta dev; output accumulates in this.output. */
  async start(): Promise<void> {
    const source = process.cwd();
    const fixtureEnv = { ...process.env, GOFLAGS: '-buildvcs=false' };
    // A fresh checkout-shaped copy: sources and configs only; node_modules
    // is symlinked so the fixture does not re-install the dependency tree.
    for (const name of ['.air.toml', 'go.mod', 'go.sum', 'package.json', 'vite.config.ts', 'tsconfig.json', 'app', 'cmd', 'internal', 'frontend', 'generated', 'routeparams', 'routecontext', 'loader', 'loaderdata', 'action']) {
      cpSync(path.join(source, name), path.join(this.root, name), { recursive: true });
    }
    symlinkSync(path.join(source, 'node_modules'), path.join(this.root, 'node_modules'), 'dir');
    mkdirSync(path.join(this.root, 'bin'));
    execFileSync('go', ['build', '-o', 'bin/meta', './cmd/meta'], { cwd: this.root, env: fixtureEnv, stdio: 'pipe' });
    this.child = spawn(path.join(this.root, 'bin/meta'), ['dev'], {
      cwd: this.root,
      env: { ...fixtureEnv, PORT: String(new URL(this.base).port), GO_META_VITE_PORT: String(this.vitePort) },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    this.child.stdout?.on('data', (data) => { this.output += data.toString(); });
    this.child.stderr?.on('data', (data) => { this.output += data.toString(); });
  }

  /** Wait until GET path answers status via Playwright's request context. */
  async waitUntil(request: { get(url: string, options?: any): Promise<{ status(): number }> }, pathname: string, status: number, timeout = 60_000): Promise<void> {
    const started = Date.now();
    while (Date.now() - started < timeout) {
      try {
        if ((await request.get(`${this.base}${pathname}`)).status() === status) return;
      } catch { /* not up yet */ }
      await pause(250);
    }
    throw Error(`${pathname} never answered ${status}; session output:\n${this.output}`);
  }

  /** SIGINT and wait for a clean exit 0; escalate to SIGKILL after a bound. */
  async stop(): Promise<void> {
    if (!this.child || this.child.exitCode !== null) return;
    this.child.kill('SIGINT');
    const exited = await Promise.race([
      new Promise<number | null>((resolve) => this.child!.once('exit', resolve)),
      pause(20_000).then(() => null),
    ]);
    if (exited === null) {
      this.child.kill('SIGKILL');
      await pause(500);
    }
  }

  /** Remove the fixture tree; safe to call twice. */
  dispose(): void {
    rmSync(this.root, { recursive: true, force: true });
  }

  /** PIDs of live vite children of the session process. */
  viteChildPids(): number[] {
    if (!this.child?.pid) return [];
    return execFileSync('ps', ['-eo', 'pid=,ppid=,args='], { encoding: 'utf8' }).split('\n').flatMap((line) => {
      const match = line.trim().match(/^(\d+)\s+(\d+)\s+(.*)$/);
      return match && Number(match[2]) === this.child!.pid && match[3].includes('/vite/bin/vite.js') ? [Number(match[1])] : [];
    });
  }

  /** True when no process from this fixture remains running. */
  processesGone(): boolean {
    const list = execFileSync('ps', ['-eo', 'args'], { encoding: 'utf8' });
    return !list.includes(`${this.root}/.tmp/air/server`) && !list.includes(`${this.root}/node_modules/vite/bin/vite.js`);
  }
}

/** Create a session with reserved ports; caller must dispose() it. */
export async function newDevSession(): Promise<DevSession> {
  const [publicPort, vitePort] = await Promise.all([freePort(), freePort()]);
  const root = mkdtempSync(path.join(tmpdir(), 'go-meta-dev-'));
  return new DevSession(root, publicPort, vitePort);
}

/** Read a fixture file as text. */
export function read(root: string, rel: string): string {
  return readFileSync(path.join(root, rel), 'utf8');
}

/** Write a fixture file (test-owned copies only, never user sources). */
export function write(root: string, rel: string, content: string): void {
  writeFileSync(path.join(root, rel), content);
}

/** Path.exists for the fixture tree. */
export function exists(root: string, rel: string): boolean {
  return existsSync(path.join(root, rel));
}
