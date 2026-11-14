import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount, tick, unmount } from 'svelte';
import RecoveryPage from '../examples/RecoveryPage.svelte';
import { recoverSession, type RecoveryClerk } from './recovery';

const data = { returnTo: '/dashboard?tab=team#members', signInURL: '/sign-in', recoveryURL: '/auth/recover', retryRequired: false };
function setup() {
  const getToken = vi.fn<(options: { skipCache: true }) => Promise<string | null>>(async () => 'private-token-never-returned');
  const getClerk = vi.fn(async (): Promise<RecoveryClerk> => ({ loaded: true, session: { getToken } }));
  const fetch = vi.fn(async () => new Response(null, { status: 204 }));
  vi.stubGlobal('fetch', fetch);
  return { ...data, getClerk, getToken, fetch, checkURL: '/auth/session' };
}
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); document.body.replaceChildren(); });

describe('Clerk browser recovery', () => {
  it('uses the initialized SDK, forces a refresh, and verifies the cookie before returning a destination', async () => {
    const options = setup();
    expect(await recoverSession(options)).toEqual({ status: 'ready', destination: data.returnTo });
    expect(options.getClerk).toHaveBeenCalledTimes(1);
    expect(options.getToken).toHaveBeenCalledWith({ skipCache: true });
    expect(options.fetch).toHaveBeenCalledWith(new URL('/auth/session', location.origin).href, expect.objectContaining({
      credentials: 'same-origin', cache: 'no-store', redirect: 'error', headers: { Accept: 'application/json' },
    }));
    expect(JSON.stringify(options.fetch.mock.calls)).not.toContain('private-token');
  });

  it('distinguishes genuinely signed-out SDK state without refreshing or probing', async () => {
    const options = setup();
    options.getClerk.mockResolvedValue({ loaded: true, session: null });
    const result = await recoverSession(options);
    expect(result.status).toBe('signed-out');
    if (result.status === 'signed-out') {
      expect(new URL(result.signInURL).origin).toBe(location.origin);
      expect(new URL(result.signInURL).searchParams.get('redirect_url')).toBe(data.returnTo);
    }
    expect(options.getToken).not.toHaveBeenCalled();
    expect(options.fetch).not.toHaveBeenCalled();
  });

  it.each([undefined, { loaded: false, session: null }])('does not treat uninitialized SDK state as sign-out: %s', async (value) => {
    const options = setup();
    options.getClerk.mockResolvedValue(value ?? { loaded: true, session: undefined });
    expect(await recoverSession(options)).toEqual({ status: 'error', reason: 'initialization' });
  });

  it.each(['load', 'refresh', 'null token', 'cookie 401', 'cookie 503', 'cookie redirect'])('fails safely with retryable UI: %s', async (kind) => {
    const options = setup();
    if (kind === 'load') options.getClerk.mockRejectedValue(new Error('SECRET credentials'));
    if (kind === 'refresh') options.getToken.mockRejectedValue(new Error('SECRET claims'));
    if (kind === 'null token') options.getToken.mockResolvedValue(null);
    if (kind === 'cookie 401') options.fetch.mockResolvedValue(new Response('SECRET cookie', { status: 401 }));
    if (kind === 'cookie 503') options.fetch.mockResolvedValue(new Response('SECRET key', { status: 503 }));
    if (kind === 'cookie redirect') options.fetch.mockRejectedValue(new TypeError('SECRET redirect'));
    const result = await recoverSession(options);
    expect(result.status).toBe('error');
    expect(JSON.stringify(result)).not.toContain('SECRET');
    expect(result).not.toHaveProperty('destination');
  });

  it.each(['https://evil.invalid', '//evil.invalid', '/\\evil.invalid', '/%2f/evil', '/%5cevil', '/%252f%252fevil', '/%0aevil', '/auth/recover', '/x/../auth/recover', '/sign-in', '/auth/session', '/\nevil'])('rejects unsafe or recursive return URL: %s', async (returnTo) => {
    const options = setup();
    expect(await recoverSession({ ...options, returnTo })).toEqual({ status: 'error', reason: 'invalid-destination' });
    expect(options.getClerk).not.toHaveBeenCalled();
  });

  it.each(['signInURL', 'recoveryURL', 'checkURL'] as const)('rejects foreign %s before SDK or fetch work', async (field) => {
    const options = setup();
    expect(await recoverSession({ ...options, [field]: 'https://evil.invalid' })).toEqual({ status: 'error', reason: 'invalid-destination' });
    expect(options.getClerk).not.toHaveBeenCalled();
  });

  it('bounds SDK initialization and cleans up the timer, ignoring late resolution', async () => {
    vi.useFakeTimers();
    const options = setup();
    let resolve!: (clerk: RecoveryClerk) => void;
    options.getClerk.mockReturnValue(new Promise<RecoveryClerk>((yes) => { resolve = yes; }));
    const pending = recoverSession({ ...options, timeoutMs: 150 });
    await vi.advanceTimersByTimeAsync(150);
    expect(await pending).toEqual({ status: 'error', reason: 'timeout' });
    resolve({ loaded: true, session: { getToken: options.getToken } });
    await Promise.resolve();
    expect(options.getToken).not.toHaveBeenCalled();
    expect(options.fetch).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('cancels pending refresh without late resume or leaving a timer', async () => {
    vi.useFakeTimers();
    const options = setup();
    options.getToken.mockReturnValue(new Promise(() => {}));
    const controller = new AbortController();
    const pending = recoverSession({ ...options, signal: controller.signal });
    await vi.advanceTimersByTimeAsync(0);
    controller.abort();
    expect(await pending).toEqual({ status: 'error', reason: 'aborted' });
    expect(vi.getTimerCount()).toBe(0);
    expect(options.fetch).not.toHaveBeenCalled();
  });

  it('requires manual retry when the server loop guard says the last resume failed', async () => {
    const options = setup();
    const target = document.createElement('div'); document.body.append(target);
    const ready = vi.fn();
    const component = mount(RecoveryPage, { target, props: {
      data: { ...data, retryRequired: true }, getClerk: options.getClerk, onReady: ready, onSignedOut: vi.fn(),
    } });
    try {
      await tick();
      expect(target.textContent).toContain('try again');
      expect(options.getClerk).not.toHaveBeenCalled();
      target.querySelector('button')!.click();
      await vi.waitFor(() => expect(ready).toHaveBeenCalledWith(data.returnTo));
    } finally { await unmount(component); }
  });

  it('renders checking, surfaces refresh failures, and cancels work when unmounted', async () => {
    const options = setup();
    let fail!: (error: unknown) => void;
    options.getToken.mockReturnValue(new Promise((_, reject) => { fail = reject; }));
    const target = document.createElement('div'); document.body.append(target);
    const ready = vi.fn();
    const component = mount(RecoveryPage, { target, props: {
      data, getClerk: options.getClerk, onReady: ready, onSignedOut: vi.fn(),
    } });
    await vi.waitFor(() => expect(options.getToken).toHaveBeenCalled());
    expect(target.textContent).toContain('Checking your session…');
    fail(new Error('SECRET refresh'));
    await vi.waitFor(() => expect(target.querySelector('[role="alert"]')).toBeTruthy());
    expect(target.textContent).not.toContain('SECRET');
    expect(ready).not.toHaveBeenCalled();
    await unmount(component);
  });
});
