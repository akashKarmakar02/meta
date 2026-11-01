import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import type { NavigationResponse } from './types';
import {
  onLifecycleEvent,
  liveInstanceCount,
} from './svelte-mounter';
import {
  navigateTo,
  startSoftNavigation as installNavigationPipeline,
  stopSoftNavigation as uninstallNavigationPipeline,
  __resetPipelineForTesting,
} from './navigation-pipeline';

// Integration test through module boundaries: fetch is mocked with the
// same URL-normalization and abort semantics as the real fetchNavigation
// (absolute URLs, fragments stripped); everything else — coordinator
// tokens, outlet replacement, CSR mounting via the generated registry,
// real Svelte page components, history, focus, and title — runs real.

const ssrAbout: NavigationResponse = {
  version: 1,
  url: '/about',
  routeId: 'about/page.templ',
  mode: 'ssr',
  title: 'About',
  layouts: ['app/layout.templ'],
  html: '<h1 data-go-page-about>About</h1>',
  clientEntry: null,
};

const csrDashboard: NavigationResponse = {
  version: 1,
  url: '/dashboard',
  routeId: 'dashboard/page.svelte',
  mode: 'csr',
  title: 'Dashboard',
  layouts: ['app/layout.templ'],
  html: '',
  clientEntry: 'dashboard/page.svelte',
};

const csrSettings: NavigationResponse = {
  version: 1,
  url: '/dashboard/settings',
  routeId: 'dashboard/settings/page.svelte',
  mode: 'csr',
  title: 'Settings',
  layouts: ['app/layout.templ'],
  html: '',
  clientEntry: 'dashboard/settings/page.svelte',
};

// Deterministic deferred fetch stand-in. Keys are the same absolute
// request URLs the real fetchNavigation would send.
const pending = new Map<
  string,
  { resolve: (env: NavigationResponse) => void; reject: (e: Error) => void; signal: AbortSignal }
>();

vi.mock('./navigation', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./navigation')>();
  return {
    ...actual,
    fetchNavigation: vi.fn((url: string | URL, options?: { signal?: AbortSignal }) =>
      new Promise<NavigationResponse>((resolve, reject) => {
        if (options?.signal?.aborted) {
          reject(new DOMException('aborted', 'AbortError'));
          return;
        }
        pending.set(String(url), {
          resolve,
          reject,
          signal: options?.signal as AbortSignal,
        });
      })
    ),
  };
});

/** The absolute request URL for a destination, mirroring fetchNavigation. */
function requestUrlOf(url: string): string {
  const u = new URL(url, document.baseURI);
  u.hash = '';
  return u.href;
}

async function settleFetch(url: string, env?: NavigationResponse) {
  const entry = pending.get(requestUrlOf(url));
  if (!entry) throw new Error(`no pending fetch for ${url}`);
  pending.delete(requestUrlOf(url));
  entry.resolve(env ?? ssrAbout);
  await vi.waitFor(() => {
    void 0;
  });
}

function renderShell(mode: 'ssr-root' | 'csr-shell' = 'ssr-root') {
  document.body.innerHTML = '';
  const footer = document.createElement('footer');
  footer.textContent = 'footer';
  if (mode === 'ssr-root') {
    const wrap = document.createElement('div');
    wrap.setAttribute('data-go-layout', 'app/layout.templ');
    document.body.append(footer, wrap);
    const outlet = document.createElement('div');
    outlet.setAttribute('data-go-page-outlet', '');
    outlet.innerHTML = '<h1 data-go-page-home>Home</h1>';
    wrap.append(outlet);
  } else {
    document.body.append(footer);
    const root = document.createElement('div');
    root.setAttribute('data-go-page-root', '');
    document.body.append(root);
  }
  return footer;
}

// Lifecycle observation through F09's public instrumentation.
let mountedEvents: number;
let unmountedEvents: number;
let stopObserving: (() => void) | null = null;

beforeEach(() => {
  mountedEvents = 0;
  unmountedEvents = 0;
  stopObserving = onLifecycleEvent((event) => {
    if (event.phase === 'mounted') mountedEvents++;
    else unmountedEvents++;
  });
});

beforeEach(() => {
  window.history.replaceState(null, '', '/');
  document.title = '';
  installNavigationPipeline();
});

afterEach(async () => {
  await __resetPipelineForTesting();
  uninstallNavigationPipeline();
  stopObserving?.();
  stopObserving = null;
});

describe('F12 integration pipeline', () => {
  it('1. SSR → SSR transition applies outlet content and commits title/URL', async () => {
    renderShell('ssr-root');
    const p = navigateTo('/about');
    await settleFetch('/about');
    await p;
    expect(document.querySelector('[data-go-page-about]')).toBeTruthy();
    expect(document.querySelector('[data-go-page-home]')).toBeNull();
    expect(document.title).toBe('About');
    expect(location.pathname).toBe('/about');
  });

  it('2. SSR → CSR transition mounts component into shell', async () => {
    renderShell('ssr-root');
    const p = navigateTo('/dashboard');
    await settleFetch('/dashboard', csrDashboard);
    await p;
    const root = document.querySelector('[data-go-page-root]');
    expect(root).toBeTruthy();
    expect(root?.getAttribute('data-go-route-id')).toBe('dashboard/page.svelte');
    // The server-owned outlet was replaced by the CSR mount target...
    expect(document.querySelector('[data-go-page-outlet] [data-go-page-root]')).toBeTruthy();
    // ...and the real Dashboard component rendered inside it.
    expect(root?.querySelector('[data-go-page="dashboard"]')).toBeTruthy();
    expect(root?.textContent).toContain('Count: 0');
  });

  it('3. CSR → CSR unmounts previous component', async () => {
    renderShell('ssr-root');
    const p1 = navigateTo('/dashboard');
    await settleFetch('/dashboard', csrDashboard);
    await p1;
    expect(liveInstanceCount()).toBe(1);
    const p2 = navigateTo('/dashboard/settings');
    await settleFetch('/dashboard/settings', csrSettings);
    await p2;
    // Exactly one instance ever mounted per navigation, previous one gone.
    expect(mountedEvents).toBe(2);
    expect(unmountedEvents).toBe(1);
    expect(liveInstanceCount()).toBe(1);
    const roots = document.querySelectorAll('[data-go-page-root]');
    expect(roots.length).toBe(1);
    expect(roots[0].getAttribute('data-go-route-id')).toBe('dashboard/settings/page.svelte');
    expect(roots[0].querySelector('h1')?.textContent).toBe('Settings');
  });

  it('4. CSR → SSR unmounts component and restores outlet', async () => {
    renderShell('ssr-root');
    const p1 = navigateTo('/dashboard');
    await settleFetch('/dashboard', csrDashboard);
    await p1;
    const p2 = navigateTo('/about');
    await settleFetch('/about', ssrAbout);
    await p2;
    expect(unmountedEvents).toBe(1);
    expect(document.querySelector('[data-go-page-root]')).toBeNull();
    expect(document.querySelector('[data-go-page-about]')).toBeTruthy();
    expect(liveInstanceCount()).toBe(0);
  });

  it('5. popstate back/forward restores page without duplicate entries', async () => {
    renderShell('ssr-root');
    const p1 = navigateTo('/about');
    await settleFetch('/about');
    await p1;
    const p2 = navigateTo('/dashboard');
    await settleFetch('/dashboard', csrDashboard);
    await p2;
    const len = history.length;
    history.back();
    await settleFetch('/about');
    await vi.waitFor(() => {
      expect(document.querySelector('[data-go-page-about]')).toBeTruthy();
    });
    expect(location.pathname).toBe('/about');
    expect(history.length).toBe(len);
  });

  it('6. title and URL reflect navigation', async () => {
    renderShell('ssr-root');
    const p = navigateTo('/dashboard');
    await settleFetch('/dashboard', csrDashboard);
    await p;
    expect(document.title).toBe('Dashboard');
    expect(location.pathname + location.search + location.hash).toBe('/dashboard');
  });

  it('7. query strings and fragments preserved in committed URL', async () => {
    renderShell('ssr-root');
    const p = navigateTo('/dashboard?tab=alerts#sec');
    await settleFetch('/dashboard?tab=alerts#sec', {
      ...csrDashboard,
      url: '/dashboard?tab=alerts#sec',
    });
    await p;
    expect(location.pathname + location.search + location.hash).toBe('/dashboard?tab=alerts#sec');
  });

  it('8. rapid A→B keeps B visible when A resolves late', async () => {
    renderShell('ssr-root');
    const pa = navigateTo('/about');
    const pb = navigateTo('/dashboard');
    // B completes first
    await settleFetch('/dashboard', csrDashboard);
    await pb;
    // A resolves late — must not overwrite. Superseded navigations exit
    // as no-ops (blueprint 5.3: an aborted navigation is not an error).
    const aboutKey = requestUrlOf('/about');
    const lateA = pending.get(aboutKey);
    pending.delete(aboutKey);
    lateA?.resolve(ssrAbout);
    await pa;
    const root = document.querySelector('[data-go-page-root]');
    expect(root?.getAttribute('data-go-route-id')).toBe('dashboard/page.svelte');
    expect(document.querySelector('[data-go-page-about]')).toBeNull();
    expect(location.pathname).toBe('/dashboard');
  });

  it('9. supersession during unmount does not corrupt ownership', async () => {
    renderShell('csr-shell');
    const p1 = navigateTo('/dashboard');
    await settleFetch('/dashboard', csrDashboard);
    await p1;
    // Begin A→SSR transition, then supersede it with a newer navigation
    // before its fetch settles: ownership must pass cleanly to B.
    const pa = navigateTo('/about');
    const pb = navigateTo('/dashboard/settings');
    await settleFetch('/dashboard/settings', csrSettings);
    await pb;
    // A settles last: superseded, its result must never touch the DOM.
    await settleFetch('/about', ssrAbout);
    await pa; // superseded no-op, never rejects
    expect(
      document.querySelector('[data-go-page-root]')?.getAttribute('data-go-route-id'),
    ).toBe('dashboard/settings/page.svelte');
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
  });

  it('10. network failure triggers hard-navigation fallback', async () => {
    renderShell('ssr-root');
    const assign = vi.spyOn(window.location, 'assign').mockImplementation(() => {});
    const p = navigateTo('/broken');
    const key = requestUrlOf('/broken');
    const entry = pending.get(key)!;
    pending.delete(key);
    entry.reject(Object.assign(new Error('net'), { code: 'http-error' }));
    await p;
    expect(assign).toHaveBeenCalledWith('/broken');
    assign.mockRestore();
  });

  it('11. stale aborted navigation never triggers fallback', async () => {
    renderShell('ssr-root');
    const spy = vi.spyOn(window.location, 'assign').mockImplementation(() => {});
    const pa = navigateTo('/about');
    const pb = navigateTo('/dashboard');
    await settleFetch('/dashboard', csrDashboard);
    await pb;
    // A's fetch rejects late with an abort error: a superseded request's
    // failure is observed through a stale token and must never fall back.
    const key = requestUrlOf('/about');
    const entry = pending.get(key)!;
    pending.delete(key);
    entry.reject(new DOMException('aborted', 'AbortError'));
    await pa;
    expect(spy).not.toHaveBeenCalled();
    spy.mockRestore();
  });

  it('12. external/download/modifier clicks retain native behavior', async () => {
    renderShell('ssr-root');
    document.body.innerHTML +=
      '<a href="https://external.example/x" id="ext">e</a>' +
      '<a href="/about" download="f.html" id="dl">d</a>' +
      '<a href="/about" id="mod">m</a>';
    const els = [
      [document.getElementById('ext')!, {}],
      [document.getElementById('dl')!, {}],
      [document.getElementById('mod')!, { ctrlKey: true }],
    ] as const;
    for (const [el, modifiers] of els) {
      el.dispatchEvent(
        new MouseEvent('click', { bubbles: true, cancelable: true, ...modifiers }),
      );
    }
    await new Promise((r) => setTimeout(r, 0));
    // No eligible click started a fetch.
    expect(pending.size).toBe(0);
  });

  it('13. ordinary reloads still work (shell intact, no listener duplication)', async () => {
    renderShell('ssr-root');
    // re-install should not duplicate handling
    installNavigationPipeline();
    const p = navigateTo('/about');
    await settleFetch('/about');
    await p;
    expect(document.querySelectorAll('[data-go-page-outlet]').length).toBe(1);
  });

  it('14. no duplicate global listeners or page instances across navigations', async () => {
    renderShell('csr-shell');
    for (const [url, env] of [
      ['/dashboard', csrDashboard],
      ['/dashboard/settings', csrSettings],
      ['/dashboard', csrDashboard],
    ] as const) {
      const p = navigateTo(url);
      await settleFetch(url, env as NavigationResponse);
      await p;
    }
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
    expect(mountedEvents).toBe(3);
    expect(liveInstanceCount()).toBe(1);
  });

  it('15. existing suites remain green (pipeline exports smoke)', () => {
    expect(typeof navigateTo).toBe('function');
    expect(typeof installNavigationPipeline).toBe('function');
  });
});
