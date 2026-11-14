import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { getNavigationState, subscribeNavigation, type NavigationState } from './navigation-state';
import { beforeNavigate } from './navigation-guards';
import { installNavigationProgress } from './navigation-progress';
import { navigateTo, startSoftNavigation, stopSoftNavigation, __setHandlesForTesting, __resetPipelineForTesting } from './navigation-pipeline';
import { NavigationError } from './navigation';
import * as imports from './route-import-map';
import type { NavigationResponse } from './types';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
const ssr = (url = '/about'): NavigationResponse => ({
  version: 1, url, routeId: 'about/page.templ', mode: 'ssr', title: 'About',
  layouts: [], html: '<h1 id="destination">About</h1>', clientEntry: null,
});
const csr = (layouts: string[] = []): NavigationResponse => ({
  ...ssr('/dashboard'), routeId: 'dashboard/page.svelte', mode: 'csr',
  html: '', clientEntry: 'dashboard/page.svelte', layouts,
});
let snapshots: NavigationState[];
let cleanup: Array<() => void>;
const fallback = vi.fn();
beforeEach(() => {
  document.body.innerHTML = '<div data-go-page-outlet><button id="outgoing">Current page</button></div>';
  history.replaceState(null, '', '/');
  fallback.mockClear();
  cleanup = [];
  snapshots = [];
  cleanup.push(subscribeNavigation((state) => snapshots.push(state)));
  snapshots.length = 0;
  __setHandlesForTesting({ hardNavigate: fallback, fetch: async () => ssr() });
});
afterEach(async () => {
  stopSoftNavigation();
  for (const stop of cleanup) stop();
  await __resetPipelineForTesting();
  __setHandlesForTesting({ hardNavigate: null, fetch: null, mountCsr: null, pushState: null });
  vi.restoreAllMocks();
  vi.useRealTimers();
});
const indicator = () => document.querySelector('[data-meta-navigation-progress]');
const terminals = () => snapshots.filter((state) => state.status === 'idle');

describe('navigation state and progress', () => {
  it('delays UI 150ms while preserving outgoing content, then removes it after commit', async () => {
    vi.useFakeTimers();
    cleanup.push(installNavigationProgress());
    const response = deferred<NavigationResponse>();
    __setHandlesForTesting({ fetch: () => response.promise });
    const navigation = navigateTo('/about');
    expect(getNavigationState().status).toBe('pending');
    await vi.advanceTimersByTimeAsync(149);
    expect(indicator()).toBeNull();
    await vi.advanceTimersByTimeAsync(1);
    expect(indicator()?.parentElement).toBe(document.body);
    expect(indicator()?.shadowRoot?.querySelector('[role="status"]')?.textContent).toBe('Loading page');
    expect(document.querySelector('#outgoing')).toBeTruthy();
    response.resolve(ssr());
    await navigation;
    expect(indicator()).toBeNull();
    expect(getNavigationState()).toMatchObject({ status: 'idle', outcome: { type: 'completed' } });
    expect(document.activeElement?.hasAttribute('data-go-page-outlet')).toBe(true);
    expect(location.pathname).toBe('/about');
    expect(terminals()).toHaveLength(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('never flashes UI for a fast navigation and cleans up before the timer fires', async () => {
    vi.useFakeTimers();
    cleanup.push(installNavigationProgress());
    await navigateTo('/about');
    await vi.advanceTimersByTimeAsync(200);
    expect(indicator()).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('guards resolve before pending/fetch; rejection emits one cancellation without loading', async () => {
    const approval = deferred<boolean>();
    cleanup.push(beforeNavigate(() => approval.promise));
    const fetch = vi.fn(async () => ssr());
    __setHandlesForTesting({ fetch });
    const navigation = navigateTo('/about');
    expect(snapshots).toHaveLength(0);
    expect(fetch).not.toHaveBeenCalled();
    approval.resolve(false);
    await navigation;
    expect(snapshots).toHaveLength(1);
    expect(getNavigationState()).toMatchObject({ status: 'idle', outcome: { type: 'canceled', reason: 'guard' } });
    expect(fetch).not.toHaveBeenCalled();
    expect(document.querySelector('#outgoing')).toBeTruthy();
    expect(location.pathname).toBe('/');
  });

  it('starts pending only after asynchronous guard approval', async () => {
    const approval = deferred<boolean>();
    const response = deferred<NavigationResponse>();
    cleanup.push(beforeNavigate(() => approval.promise));
    __setHandlesForTesting({ fetch: () => response.promise });
    const navigation = navigateTo('/about');
    approval.resolve(true);
    await vi.waitFor(() => expect(getNavigationState().status).toBe('pending'));
    response.resolve(ssr());
    await navigation;
    expect(terminals()).toHaveLength(1);
  });

  it.each([false, true])('supports AbortSignal cancellation (already aborted: %s), no duplicate terminal/fallback', async (already) => {
    vi.useFakeTimers();
    cleanup.push(installNavigationProgress());
    const response = deferred<NavigationResponse>();
    __setHandlesForTesting({ fetch: () => response.promise });
    const controller = new AbortController();
    if (already) controller.abort();
    const navigation = navigateTo('/about', { signal: controller.signal });
    if (!already) {
      await vi.advanceTimersByTimeAsync(150);
      expect(indicator()).toBeTruthy();
      controller.abort();
    }
    expect(indicator()).toBeNull();
    response.resolve(ssr()); // even a request ignoring abort cannot commit
    await navigation;
    expect(getNavigationState()).toMatchObject({ outcome: { type: 'canceled', reason: 'aborted' } });
    expect(terminals()).toHaveLength(1);
    expect(fallback).not.toHaveBeenCalled();
    expect(document.querySelector('#outgoing')).toBeTruthy();
  });

  it('recognizes fetchNavigation typed abort failures', async () => {
    __setHandlesForTesting({ fetch: async () => { throw new NavigationError('aborted', 'canceled'); } });
    await navigateTo('/about');
    expect(getNavigationState()).toMatchObject({ outcome: { type: 'canceled', reason: 'aborted' } });
    expect(fallback).not.toHaveBeenCalled();
  });

  it('clears visible progress synchronously before handing a failure to the browser', async () => {
    vi.useFakeTimers();
    cleanup.push(installNavigationProgress());
    const response = deferred<NavigationResponse>();
    __setHandlesForTesting({ fetch: () => response.promise });
    fallback.mockImplementationOnce(() => {
      expect(indicator()).toBeNull();
      expect(getNavigationState()).toMatchObject({ status: 'idle', outcome: { type: 'failed' } });
      expect(vi.getTimerCount()).toBe(0);
      expect(document.querySelector('#outgoing')).toBeTruthy();
    });
    const navigation = navigateTo('/about');
    await vi.advanceTimersByTimeAsync(150);
    expect(indicator()).toBeTruthy();
    response.reject(new Error('offline'));
    await navigation;
    expect(fallback).toHaveBeenCalledTimes(1);
    expect(terminals()).toHaveLength(1);
  });

  it('superseded requests cannot clear a newer pending state or emit duplicate terminals', async () => {
    const first = deferred<NavigationResponse>();
    const second = deferred<NavigationResponse>();
    __setHandlesForTesting({ fetch: vi.fn().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise) });
    const a = navigateTo('/a');
    const aID = getNavigationState().attempt!.id;
    const b = navigateTo('/b');
    const bState = getNavigationState();
    expect(bState.status).toBe('pending');
    expect(bState.attempt!.id).toBeGreaterThan(aID);
    first.reject(new Error('late failure'));
    await a;
    expect(getNavigationState()).toBe(bState);
    second.resolve(ssr('/b'));
    await b;
    expect(terminals().map((state) => state.outcome?.type)).toEqual(['canceled', 'completed']);
    expect(fallback).not.toHaveBeenCalled();
  });

  it('cancels an unresolved guard when superseded, without ever starting its loading', async () => {
    const approval = deferred<boolean>();
    cleanup.push(beforeNavigate((attempt) => attempt.url.endsWith('/a') ? approval.promise : true));
    const a = navigateTo('/a');
    await navigateTo('/b');
    const committed = getNavigationState();
    approval.resolve(true);
    await a;
    expect(getNavigationState()).toBe(committed);
    expect(snapshots.filter((state) => state.status === 'pending')).toHaveLength(1);
    expect(terminals()).toHaveLength(2);
  });

  it('keeps pending through page/layout downloads, using each prepared import once', async () => {
    const page = deferred<imports.PageComponent | null>();
    const layout = deferred<imports.LayoutComponent | null>();
    const pageComponent = await imports.loadPage('dashboard/page.svelte');
    const layoutComponent = await imports.loadLayout('app/dashboard/layout.svelte');
    const pageLoad = vi.spyOn(imports, 'loadPage').mockReturnValue(page.promise);
    const layoutLoad = vi.spyOn(imports, 'loadLayout').mockReturnValue(layout.promise);
    __setHandlesForTesting({ fetch: async () => csr(['app/dashboard/layout.svelte']) });
    const navigation = navigateTo('/dashboard');
    await vi.waitFor(() => expect(pageLoad).toHaveBeenCalled());
    expect(document.querySelector('#outgoing')).toBeTruthy();
    page.resolve(pageComponent);
    await vi.waitFor(() => expect(layoutLoad).toHaveBeenCalled());
    expect(getNavigationState().status).toBe('pending');
    expect(document.querySelector('#outgoing')).toBeTruthy();
    layout.resolve(layoutComponent);
    await navigation;
    expect(pageLoad).toHaveBeenCalledTimes(1);
    expect(layoutLoad).toHaveBeenCalledTimes(1);
    expect(document.querySelector('[data-go-page="dashboard"]')).toBeTruthy();
    expect(getNavigationState()).toMatchObject({ outcome: { type: 'completed' } });
  });

  it('a stale slow import does not block a newer commit', async () => {
    const page = deferred<imports.PageComponent | null>();
    const load = vi.spyOn(imports, 'loadPage').mockReturnValue(page.promise);
    __setHandlesForTesting({ fetch: async (url) => String(url).endsWith('/dashboard') ? csr() : ssr('/b') });
    const a = navigateTo('/dashboard');
    await vi.waitFor(() => expect(load).toHaveBeenCalled());
    await navigateTo('/b');
    expect(location.pathname).toBe('/b');
    const committed = getNavigationState();
    page.reject(new Error('stale download failed'));
    await a;
    expect(getNavigationState()).toBe(committed);
    expect(fallback).not.toHaveBeenCalled();
  });

  it.each(['network', 'page import', 'layout import', 'mount', 'guard'])('reports %s failure once, clears UI before full-document fallback', async (kind) => {
    const error = new Error(kind);
    if (kind === 'network') __setHandlesForTesting({ fetch: async () => { throw error; } });
    if (kind === 'guard') cleanup.push(beforeNavigate(() => { throw error; }));
    if (kind === 'page import') vi.spyOn(imports, 'loadPage').mockRejectedValue(error);
    if (kind === 'layout import') vi.spyOn(imports, 'loadLayout').mockRejectedValue(error);
    if (kind === 'mount') __setHandlesForTesting({ mountCsr: async () => { throw error; } });
    if (kind.includes('import') || kind === 'mount') {
      __setHandlesForTesting({ fetch: async () => csr(kind === 'layout import' ? ['app/dashboard/layout.svelte'] : []) });
    }
    await navigateTo('/dashboard');
    expect(terminals()).toHaveLength(1);
    expect(getNavigationState()).toMatchObject({ status: 'idle', outcome: { type: 'failed', error } });
    expect(fallback).toHaveBeenCalledTimes(1);
    if (kind !== 'mount') expect(document.querySelector('#outgoing')).toBeTruthy();
  });

  it('distinguishes a required full-document transition from completion', async () => {
    document.body.insertAdjacentHTML('beforeend', '<div data-go-layout="app/dashboard/layout.svelte"></div>');
    await navigateTo('/about');
    expect(getNavigationState()).toMatchObject({ outcome: { type: 'fallback' } });
    expect(fallback).toHaveBeenCalledWith('/about');
    expect(terminals()).toHaveLength(1);
  });

  it('stays pending until destination mounting settles', async () => {
    const mount = deferred<Awaited<ReturnType<typeof import('./csr-mount').mountCsrPage>>>();
    const mounted = vi.fn(() => mount.promise);
    __setHandlesForTesting({ fetch: async () => csr(), mountCsr: mounted });
    const navigation = navigateTo('/dashboard');
    await vi.waitFor(() => expect(mounted).toHaveBeenCalled());
    expect(getNavigationState().status).toBe('pending');
    expect(location.pathname).toBe('/');
    const root = document.createElement('div');
    document.body.append(root);
    mount.resolve({ target: root, pageRoot: root, mounted: true, unmount: async () => {} });
    await navigation;
    expect(getNavigationState()).toMatchObject({ outcome: { type: 'completed' } });
  });

  it('supports opt-out and removes timers/subscriptions on stop, including pending cancellation', async () => {
    vi.useFakeTimers();
    startSoftNavigation({ progress: false });
    const response = deferred<NavigationResponse>();
    __setHandlesForTesting({ fetch: () => response.promise });
    const navigation = navigateTo('/about');
    await vi.advanceTimersByTimeAsync(200);
    expect(indicator()).toBeNull();
    const stop = installNavigationProgress();
    expect(vi.getTimerCount()).toBe(1);
    stop();
    expect(vi.getTimerCount()).toBe(0);
    stopSoftNavigation();
    response.resolve(ssr());
    await navigation;
    expect(getNavigationState()).toMatchObject({ outcome: { type: 'canceled', reason: 'stopped' } });
    startSoftNavigation();
    await navigateTo('/about');
    stopSoftNavigation();
    expect(indicator()).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('fragment-only programmatic navigation emits no state changes or fetch', async () => {
    const fetch = vi.fn(async () => ssr());
    __setHandlesForTesting({ fetch });
    await navigateTo('/#anchor');
    expect(fetch).not.toHaveBeenCalled();
    expect(snapshots).toHaveLength(0);
    expect(location.hash).toBe('#anchor');
  });

  it('isolates throwing observers and unsubscribes immediately', async () => {
    const bad = subscribeNavigation(() => { throw new Error('observer'); });
    const listener = vi.fn();
    const stop = subscribeNavigation(listener);
    expect(listener).toHaveBeenCalledTimes(1);
    stop(); bad();
    await navigateTo('/about');
    expect(listener).toHaveBeenCalledTimes(1);
    expect(getNavigationState().status).toBe('idle');
  });

  it('a subscriber can start a new navigation without reordering notifications or allowing stale fetch', async () => {
    let replacement: Promise<void> | undefined;
    cleanup.push(subscribeNavigation((state) => {
      if (state.status === 'pending' && state.attempt.url.endsWith('/a')) {
        replacement = navigateTo('/b');
      }
    }));
    const fetch = vi.fn(async (url: string | URL) => ssr(new URL(url).pathname));
    __setHandlesForTesting({ fetch });
    snapshots.length = 0;
    await navigateTo('/a');
    await replacement;
    expect(snapshots.map((state) => [state.attempt?.url.split('/').at(-1), state.status])).toEqual([
      ['a', 'pending'], ['a', 'idle'], ['b', 'pending'], ['b', 'idle'],
    ]);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(location.pathname).toBe('/b');
    expect(fallback).not.toHaveBeenCalled();
  });

  it('subscriptions added during cancellation get one terminal snapshot without replaying queued events', async () => {
    const controller = new AbortController();
    const lateListener = vi.fn();
    cleanup.push(subscribeNavigation((state) => {
      if (state.status === 'pending') {
        controller.abort();
        cleanup.push(subscribeNavigation(lateListener));
      }
    }));
    await navigateTo('/about', { signal: controller.signal });
    expect(lateListener).toHaveBeenCalledTimes(1);
    expect(lateListener).toHaveBeenCalledWith(expect.objectContaining({ outcome: { type: 'canceled', reason: 'aborted' } }));
    expect(terminals()).toHaveLength(1);
  });

  it('records history traversals without duplicate entries, and rejects Back without loading', async () => {
    startSoftNavigation({ progress: false });
    __setHandlesForTesting({ fetch: async (url) => ssr(new URL(url).pathname) });
    await navigateTo('/a');
    await navigateTo('/b');
    const length = history.length;
    history.back();
    await vi.waitFor(() => expect(getNavigationState()).toMatchObject({ status: 'idle', attempt: { source: 'history', url: 'http://localhost:3000/a' }, outcome: { type: 'completed' } }));
    history.forward();
    await vi.waitFor(() => expect(location.pathname).toBe('/b'));
    await vi.waitFor(() => expect(getNavigationState().status).toBe('idle'));
    expect(history.length).toBe(length);
    cleanup.push(beforeNavigate(() => false));
    snapshots.length = 0;
    history.back();
    await vi.waitFor(() => expect(getNavigationState()).toMatchObject({ outcome: { type: 'canceled', reason: 'guard' } }));
    await vi.waitFor(() => expect(location.pathname).toBe('/b'));
    expect(snapshots.every((state) => state.status === 'idle')).toBe(true);
    expect(history.length).toBe(length);
  });
});
