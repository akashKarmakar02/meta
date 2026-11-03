// Shutdown and supervision (P2-A06 §13): SIGINT stops every child process,
// removes the private socket directory, and leaves no fixture processes
// behind. A second meta dev on the same port fails with a useful error.
import { test, expect } from '@playwright/test';
import { spawn, execFileSync } from 'node:child_process';
import { readFileSync, existsSync } from 'node:fs';
import path from 'node:path';
import { newDevSession, pause, type DevSession } from './fixtures/dev-session';

let session: DevSession;

test.beforeAll(async () => {
  session = await newDevSession();
  await session.start();
});

test.afterAll(async () => {
  await session.stop();
  session.dispose();
});

test('a second meta dev on the same public port fails with a useful error', async ({ request }) => {
  await session.waitUntil(request, '/healthz', 200);
  const env = { ...process.env, GOFLAGS: '-buildvcs=false', PORT: String(new URL(session.base).port), GO_META_VITE_PORT: String(session.vitePort + 1) };
  const blocked = spawn(path.join(session.root, 'bin/meta'), ['dev'], { cwd: session.root, env });
  let conflict = '';
  blocked.stdout.on('data', (d) => { conflict += d.toString(); });
  blocked.stderr.on('data', (d) => { conflict += d.toString(); });
  try {
    const status = await Promise.race([
      new Promise<number | null>((resolve) => blocked.once('exit', resolve)),
      pause(20_000).then(() => { throw Error('port conflict did not stop meta dev'); }),
    ]);
    expect(status).not.toBe(0);
    expect(conflict).toMatch(/cannot start public development server/);
    expect(conflict).toMatch(/address already in use/);
  } finally {
    if (blocked.exitCode === null) blocked.kill('SIGKILL');
  }
});

test('SIGINT exits cleanly and removes every child and the private socket', async ({ request }) => {
  await session.waitUntil(request, '/healthz', 200);
  // Locate the socket directory through the backend process's environment.
  const serverLine = execFileSync('ps', ['-eo', 'pid=,args='], { encoding: 'utf8' })
    .split('\n').find((line) => line.includes(`${session.root}/.tmp/air/server`));
  expect(serverLine).toBeTruthy();
  const serverPID = Number(serverLine!.trim().split(/\s+/)[0]);
  const socketEnv = readFileSync(`/proc/${serverPID}/environ`, 'utf8').split('\0').find((v) => v.startsWith('GO_META_DEV_SOCKET='));
  expect(socketEnv).toBeTruthy();
  const socketDir = path.dirname(socketEnv!.slice('GO_META_DEV_SOCKET='.length));
  expect(existsSync(socketDir)).toBe(true);

  await session.stop();
  expect(session.child?.exitCode).toBe(0);
  await expect.poll(() => session.processesGone(), { timeout: 10_000 }).toBe(true);
  expect(existsSync(socketDir)).toBe(false);
  // Public proxy and Vite are gone too.
  await expect.poll(async () => { try { await request.get(`${session.base}/healthz`, { timeout: 500 }); return true; } catch { return false; } }).toBe(false);
  await expect.poll(async () => { try { await request.get(`http://127.0.0.1:${session.vitePort}/@vite/client`, { timeout: 500 }); return true; } catch { return false; } }).toBe(false);
});
