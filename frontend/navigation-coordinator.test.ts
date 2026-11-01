// F11 tests: abort + stale response navigation IDs (blueprint 5.3).
// The acceptance scenario — an artificial delayed /about envelope that
// resolves late cannot overwrite a newer /dashboard — is exercised both
// at coordinator level and through a miniature pipeline that mimics
// F12's shape: begin -> fetch(signal) -> [await] -> assertCurrent ->
// apply DOM. fetchNavigation itself is used with real deferred promises.
import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
  beginNavigation,
  assertCurrent,
  commitIfCurrent,
  currentNavigationId,
  StaleNavigationError,
} from './navigation-coordinator';
import { fetchNavigation, NavigationError } from './navigation';
import type { NavigationResponse } from './types';
import {
  pushNavigationState,
  installPopstateNavigation,
  uninstallPopstateNavigation,
} from './navigation-history';

/** A fetch mock whose responses resolve in caller-controlled order. */
function deferredFetch() {
  const pending: Array<{
    input: RequestInfo | URL;
    init: RequestInit | undefined;
    resolve: (r: Response) => void;
    reject: (e: unknown) => void;
  }> = [];
  const calls = () => pending.map((p) => ({ input: p.input, init: p.init }));
  const stub = vi.fn(
    (input: RequestInfo | URL, init?: RequestInit) =>
      new Promise<Response>((resolve, reject) => {
        pending.push({ input, init, resolve, reject });
      })
  );
  return {
    stub,
    calls,
    /** Deliver the n-th request as a valid envelope. */
    resolveEnvelope(n: number, env: NavigationResponse) {
      pending[n].resolve(
        new Response(JSON.stringify(env), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );
    },
    /** Fail the n-th request. */
    rejectCall(n: number, err: unknown) {
      pending[n].reject(err);
    },
  };
}

function ssrEnv(url: string, html: string): NavigationResponse {
  return {
    version: 1,
    url,
    routeId: 'about/page.templ',
    mode: 'ssr',
    title: 'About',
    layouts: ['app/layout.templ'],
    html,
    clientEntry: null,
  };
}

function csrEnv(url: string, entry: string): NavigationResponse {
  return {
    version: 1,
    url,
    routeId: entry,
    mode: 'csr',
    title: 'Dashboard',
    layouts: ['app/layout.templ'],
    html: '',
    clientEntry: entry,
  };
}

describe('F11: navigation ids and abort (5.3)', () => {
  it('issues monotonically increasing ids', () => {
    const a = beginNavigation();
    const b = beginNavigation();
    const c = beginNavigation();
    expect(a.id).toBeLessThan(b.id);
    expect(b.id).toBeLessThan(c.id);
    expect(currentNavigationId()).toBe(c.id);
    expect(a.isCurrent()).toBe(false);
    expect(b.isCurrent()).toBe(false);
    expect(c.isCurrent()).toBe(true);
  });

  it('starting navigation B aborts navigation A\'s signal', async () => {
    const a = beginNavigation();
    const aborted = new Promise<boolean>((r) =>
      a.signal.addEventListener('abort', () => r(true))
    );
    expect(a.signal.aborted).toBe(false);
    const b = beginNavigation();
    expect(await aborted).toBe(true);
    expect(a.signal.aborted).toBe(true);
    expect(b.signal.aborted).toBe(false);
  });

  it("completing an older navigation cannot clear the newer one's controller", () => {
    const a = beginNavigation();
    const b = beginNavigation();
    // A late abort() from the superseded navigation must not disturb B.
    a.abort();
    expect(b.signal.aborted).toBe(false);
    expect(b.isCurrent()).toBe(true);
  });
});

describe('F11: stale commits rejected (the acceptance scenario)', () => {
  it('artificial delayed /about cannot overwrite newer /dashboard', async () => {
    // Miniature pipeline in F12's shape, against REAL modules:
    // fetchNavigation, replacePageOutlet, pushNavigationState.
    const { replacePageOutlet } = await import('./ssr-outlet');

    document.body.innerHTML =
      '<div data-go-page-outlet><main id="old">Home</main></div><footer>site</footer>';

    const df = deferredFetch();
    const originalFetch = globalThis.fetch;
    globalThis.fetch = df.stub as typeof fetch;

    try {
      window.location.href = 'http://localhost/';
      history.replaceState(null, '', '/');

      // Navigation A: /about (will be delayed).
      const tokenA = beginNavigation();
      const promiseA = fetchNavigation('/about', { signal: tokenA.signal });

      // Navigation B: /dashboard starts immediately after.
      const tokenB = beginNavigation();
      const promiseB = fetchNavigation('/dashboard', { signal: tokenB.signal });

      // A's signal was aborted by B...
      expect(tokenA.signal.aborted).toBe(true);

      // B resolves first and applies cleanly.
      df.resolveEnvelope(1, csrEnv('/dashboard', 'dashboard/page.svelte'));
      const envB = await promiseB;
      expect(envB.clientEntry).toBe('dashboard/page.svelte');
      assertCurrent(tokenB); // still current

      // Now the DELAYED A response finally arrives, ignoring its abort.
      df.resolveEnvelope(0, ssrEnv('/about', '<main data-go-page-about>About</main>'));
      let staleApplied = false;
      try {
        const envA = await promiseA; // may resolve despite the abort
        assertCurrent(tokenA); // must throw
        replacePageOutlet(envA); // would be the bug
        staleApplied = true;
      } catch (e) {
        expect(e).toBeInstanceOf(StaleNavigationError);
      }
      expect(staleApplied).toBe(false);

      // Outlet still shows what? Nothing was applied for A; B was CSR so
      // this miniature didn't render it into the outlet — the pinned
      // fact is that A's html is NOT present.
      expect(document.body.innerHTML).not.toContain('data-go-page-about');

      // B's history commit goes through.
      pushNavigationState(envB);
      expect(location.pathname).toBe('/dashboard');
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it('a late-rejecting stale request cannot trigger fallback', async () => {
    // F12 will implement fallback as: on NavigationError, hard-navigate.
    // The guard demonstrated here: a pipeline that re-checks currency
    // after the rejection treats a STALE failure as a no-op, never an
    // error path.
    const originalFetch = globalThis.fetch;
    const df = deferredFetch();
    globalThis.fetch = df.stub as typeof fetch;

    try {
      const tokenA = beginNavigation();
      const promiseA = fetchNavigation('/about', { signal: tokenA.signal });
      const tokenB = beginNavigation();
      const promiseB = fetchNavigation('/dashboard', { signal: tokenB.signal });
      df.resolveEnvelope(1, csrEnv('/dashboard', 'dashboard/page.svelte'));

      let fallbackAttempts = 0;
      const attemptFallback = (token: { isCurrent(): boolean }) => {
        if (!token.isCurrent()) return; // stale failure: never fall back
        fallbackAttempts++;
      };

      // B succeeds; then A rejects LATE with a network error.
      await promiseB;
      df.rejectCall(0, new TypeError('network down'));
      try {
        await promiseA;
      } catch (e) {
        attemptFallback(tokenA); // observes staleness, does nothing
      }
      expect(fallbackAttempts).toBe(0);

      // An aborted-then-resolving navigation is equally not a failure:
      df.resolveEnvelope(0, undefined as never);
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it('repeated rapid navigations select only the newest result', async () => {
    const originalFetch = globalThis.fetch;
    const df = deferredFetch();
    globalThis.fetch = df.stub as typeof fetch;
    try {
      const tokens = [0, 1, 2, 3].map(() => beginNavigation());
      const promises = tokens.map((t) =>
        fetchNavigation(`/page-${t.id}`, { signal: t.signal })
      );
      // Resolve ALL of them, newest first.
      for (let i = 3; i >= 0; i--) {
        df.resolveEnvelope(i, csrEnv(`/page-${i}`, `page-${i}/page.svelte`));
      }
      const results = await Promise.allSettled(promises);
      // Every request may well resolve (abort is cooperative), but only
      // the newest token stays current.
      expect(tokens[3].isCurrent()).toBe(true);
      expect(tokens.slice(0, 3).every((t) => !t.isCurrent())).toBe(true);
      // Applied results: only the newest token's id.
      const applied: number[] = [];
      results.forEach((r, i) => {
        commitIfCurrent(tokens[i], () => applied.push(tokens[i].id));
      });
      expect(applied).toEqual([tokens[3].id]);
    } finally {
      globalThis.fetch = originalFetch;
    }
  });
});

describe('F11: stale guards on every commit surface', () => {
  beforeEach(() => {
    window.location.href = 'http://localhost/';
    history.replaceState(null, '', '/');
    document.title = '';
    document.body.innerHTML =
      '<div data-go-page-outlet><main>Home</main></div>';
  });

  it('stale SSR replacement is refused by assertCurrent ordering', async () => {
    const { replacePageOutlet } = await import('./ssr-outlet');
    const tokenA = beginNavigation();
    beginNavigation(); // supersedes A

    // Delayed A envelope arrives late:
    const envA = ssrEnv('/about', '<main data-go-page-about>ABOUT</main>');
    expect(() => {
      assertCurrent(tokenA);
      replacePageOutlet(envA);
    }).toThrow(StaleNavigationError);
    expect(document.querySelector('[data-go-page-about]')).toBeNull();
  });

  it('stale history/title/scroll mutation is refused', () => {
    const tokenA = beginNavigation();
    beginNavigation();

    const env = ssrEnv('/about', '<main>About</main>');
    const beforeLength = history.length;
    expect(() => {
      assertCurrent(tokenA);
      pushNavigationState(env); // sets URL, title, scroll
    }).toThrow(StaleNavigationError);
    expect(history.length).toBe(beforeLength);
    expect(location.pathname).toBe('/');
    expect(document.title).toBe('');
  });

  it('CSR dynamic-import delay cannot allow stale mounts', async () => {
    // The registry import is async; simulate a SLOW import where the
    // component resolves only after a newer navigation began.
    const tokenA = beginNavigation();
    let releaseImport!: (c: unknown) => void;
    const slowImport = new Promise<unknown>((r) => (releaseImport = r));

    beginNavigation(); // supersedes A while its import is in flight

    releaseImport({ render: () => ({}) });
    const component = await slowImport;
    // Import resolved, but A is stale — mounting must be refused.
    expect(tokenA.isCurrent()).toBe(false);
    expect(() => assertCurrent(tokenA)).toThrow(StaleNavigationError);
    expect(component).toBeTruthy(); // the import itself is harmless
  });

  it('a superseded popstate handler chain is naturally safe (no entries)', async () => {
    // popstate restoration itself never pushes; combined with currency
    // checks, back/forward races stay clean. Pinned here end-to-end.
    const seen: string[] = [];
    installPopstateNavigation((ctx) => {
      seen.push(ctx.url.pathname);
    });
    const t1 = beginNavigation();
    pushNavigationState(ssrEnv('/about', '<main>A</main>'));
    assertCurrent(t1); // fine
    history.back();
    await new Promise((r) => setTimeout(r, 40));
    expect(seen).toEqual(['/']);
    uninstallPopstateNavigation();
  });
});
