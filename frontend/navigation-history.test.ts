// F10 tests: history.pushState + popstate behavior (blueprint 5.2
// steps 10-11). "Back/forward restores right route" is exercised via
// the module's contracts: correct URL commits, query/hash preservation,
// duplicate-entry avoidance, failure-free history on bad envelopes,
// and popstate restoration without pushState.
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import {
  pushNavigationState,
  installPopstateNavigation,
  uninstallPopstateNavigation,
  HistoryError,
  type PopstateContext,
} from './navigation-history';
import type { NavigationResponse } from './types';

function envelope(partial: Partial<NavigationResponse> = {}): NavigationResponse {
  return {
    version: 1,
    url: '/dashboard',
    routeId: 'dashboard/page.svelte',
    mode: 'csr',
    title: 'Dashboard',
    layouts: ['app/layout.templ'],
    html: '',
    clientEntry: 'dashboard/page.svelte',
    ...partial,
  } as NavigationResponse;
}

describe('F10: pushNavigationState (step 10)', () => {
  beforeEach(() => {
    window.location.href = 'http://localhost/';
    window.history.replaceState(null, '', '/');
    document.title = '';
  });

  it('writes pathname, query, and hash of a successful navigation', () => {
    pushNavigationState(
      envelope({ url: '/products/42?tab=specs#reviews' })
    );
    expect(window.location.pathname).toBe('/products/42');
    expect(window.location.search).toBe('?tab=specs');
    expect(window.location.hash).toBe('#reviews');
  });

  it('reflects same-origin redirects via the envelope url', () => {
    // fetchNavigation follows redirects; env.url is where we landed.
    pushNavigationState(
      envelope({
        url: '/dashboard/settings?from=legacy-profile',
        mode: 'csr',
        clientEntry: 'dashboard/settings/page.svelte',
      })
    );
    expect(window.location.pathname).toBe('/dashboard/settings');
    expect(window.location.search).toBe('?from=legacy-profile');
  });

  it('sets document.title from the envelope', () => {
    pushNavigationState(envelope({ title: 'Settings' }));
    expect(document.title).toBe('Settings');
  });

  it('resets scroll to top without a hash', () => {
    window.scrollTo(0, 500);
    expect(window.scrollY).toBe(500);
    pushNavigationState(envelope({ url: '/about', mode: 'ssr' }));
    expect(window.scrollY).toBe(0);
  });

  it('remembers the left page scroll and restores it on return', async () => {
    window.scrollTo(0, 300);
    pushNavigationState(envelope({ url: '/about', mode: 'ssr' }));
    // Leaving "/" at scroll 300; /about resets to top.
    expect(window.scrollY).toBe(0);

    const received: PopstateContext[] = [];
    installPopstateNavigation((ctx) => received.push(ctx));
    window.history.back();
    await new Promise((r) => setTimeout(r, 40));
    uninstallPopstateNavigation();

    expect(window.location.pathname).toBe('/');
    expect(received).toHaveLength(1);
    expect(received[0].url.pathname).toBe('/');
    expect(received[0].scroll).toEqual({ x: 0, y: 300 });
  });

  it('replaces instead of stacking a duplicate destination', () => {
    window.history.pushState(null, '', '/start');
    const before = window.history.length;
    pushNavigationState(envelope({ url: '/dashboard' }));
    const mid = window.history.length;
    expect(mid).toBe(before + 1);
    // Committing the SAME destination again must not add an entry.
    pushNavigationState(envelope({ url: '/dashboard' }));
    expect(window.history.length).toBe(mid);
    expect(window.location.pathname).toBe('/dashboard');
  });

  it('rejects foreign origins without touching history', () => {
    const before = window.history.length;
    expect(() =>
      pushNavigationState(envelope({ url: 'https://evil.example.com/x' }))
    ).toThrow(HistoryError);
    expect(window.history.length).toBe(before);
    expect(window.location.hostname).not.toBe('evil.example.com');
  });

  it('rejects invalid envelopes without touching history', () => {
    const before = window.history.length;
    expect(() =>
      pushNavigationState({ ...envelope(), url: '' })
    ).toThrow(HistoryError);
    expect(() =>
      pushNavigationState(null as unknown as NavigationResponse)
    ).toThrow(HistoryError);
    expect(window.history.length).toBe(before);
  });

  it('works for both SSR and CSR destinations', () => {
    pushNavigationState(envelope({ url: '/about', mode: 'ssr', html: '<main data-go-page-about>About</main>', clientEntry: null }));
    expect(window.location.pathname).toBe('/about');
    pushNavigationState(envelope({ url: '/dashboard', mode: 'csr' }));
    expect(window.location.pathname).toBe('/dashboard');
  });
});

describe('F10: popstate (step 11)', () => {
  let handlerCalls: PopstateContext[];

  beforeEach(() => {
    window.location.href = 'http://localhost/';
    window.history.replaceState(null, '', '/');
    handlerCalls = [];
  });

  afterEach(() => {
    uninstallPopstateNavigation();
  });

  it('hands the restored URL to the handler without pushing history', async () => {
    window.history.pushState(null, '', '/docs');
    installPopstateNavigation((ctx) => handlerCalls.push(ctx));

    const before = window.history.length;
    window.history.back();
    await new Promise((r) => setTimeout(r, 40));

    expect(handlerCalls).toHaveLength(1);
    expect(handlerCalls[0].url.pathname).toBe('/');
    // Restoration never appends entries.
    expect(window.history.length).toBe(before);
  });

  it('back/forward resolve the intended destinations', async () => {
    pushNavigationState(envelope({ url: '/about', mode: 'ssr' }));
    pushNavigationState(envelope({ url: '/dashboard' }));
    installPopstateNavigation((ctx) => handlerCalls.push(ctx));

    window.history.back();
    await new Promise((r) => setTimeout(r, 40));
    expect(window.location.pathname).toBe('/about');
    expect(handlerCalls.at(-1)?.url.pathname).toBe('/about');

    window.history.back();
    await new Promise((r) => setTimeout(r, 40));
    expect(window.location.pathname).toBe('/');
    expect(handlerCalls.at(-1)?.url.pathname).toBe('/');

    window.history.forward();
    await new Promise((r) => setTimeout(r, 40));
    expect(window.location.pathname).toBe('/about');
    expect(handlerCalls.at(-1)?.url.pathname).toBe('/about');
  });

  it('replaces the handler instead of stacking listeners', async () => {
    installPopstateNavigation(() =>
      handlerCalls.push({ url: null as never, scroll: { x: 0, y: 0 }, sameDocument: false })
    );
    installPopstateNavigation((ctx) => handlerCalls.push(ctx));
    handlerCalls = [];

    window.history.pushState(null, '', '/x');
    window.history.back();
    await new Promise((r) => setTimeout(r, 40));

    // Only the latest handler ran — exactly once.
    expect(handlerCalls).toHaveLength(1);
    expect(handlerCalls[0].url).not.toBeNull();
  });

  it('goes dormant after uninstall', async () => {
    installPopstateNavigation((ctx) => handlerCalls.push(ctx));
    uninstallPopstateNavigation();

    window.history.pushState(null, '', '/y');
    window.history.back();
    await new Promise((r) => setTimeout(r, 40));
    expect(handlerCalls).toHaveLength(0);
  });
});
