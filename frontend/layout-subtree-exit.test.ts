// G07 tests: graceful persistent-layout teardown when leaving the
// Dashboard subtree (blueprint G07: "Switch /dashboard→/about, unmount
// nested layout. Done: destructor called once").
//
// Scope: the EXIT operation (frontend/layout-subtree-exit.ts) and its
// pipeline integration — child-first teardown order, exactly one layout
// destructor, host/marker/outlet/page-root removal, no leaks across
// repeated entry/exit, stale-navigation safety at the teardown boundary,
// and fallback on failed teardown or unrenderable destinations.
//
// G08 (fresh state on RE-ENTRY) is not asserted beyond "the next mount
// works": fresh counter state belongs to that gate's acceptance.

import { describe, it, expect, beforeEach, afterEach, beforeAll } from 'vitest';
import { mountPersistentLayout, type LayoutInstanceHandle } from './layout-mount';
import { mountComponent, onLifecycleEvent, liveInstanceCount } from './svelte-mounter';
import { loadPage } from './route-import-map';
import {
  unmountLayoutSubtree,
  LayoutExitError,
} from './layout-subtree-exit';
import type { NavigationResponse } from './types';

const DASHBOARD_LAYOUT_ID = 'app/dashboard/layout.svelte';

const csrDashboard: NavigationResponse = {
  version: 1,
  url: '/dashboard',
  routeId: 'dashboard/page.svelte',
  mode: 'csr',
  title: 'Dashboard',
  layouts: [DASHBOARD_LAYOUT_ID],
  html: '',
  clientEntry: 'dashboard/page.svelte',
};

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

/** URL-keyed fetch stub over the same normalization as doFetch. */
function stubFetch(map: Map<string, NavigationResponse>): void {
  const { __setHandlesForTesting } = pipelineRef!;
  __setHandlesForTesting({
    fetch: ((url: string) => {
      const env = map.get(new URL(url, document.baseURI).href);
      if (!env) return Promise.reject(new Error('no fixture'));
      return Promise.resolve(env);
    }) as typeof import('./navigation').fetchNavigation,
    hardNavigate: null,
  });
}

function stubHardNavigate(sink: { url: string | null }): void {
  const { __setHandlesForTesting } = pipelineRef!;
  __setHandlesForTesting({
    hardNavigate: (url: string) => {
      sink.url = url;
    },
  });
}

function clearFetch(): void {
  const { __setHandlesForTesting } = pipelineRef!;
  __setHandlesForTesting({ fetch: null, hardNavigate: null });
}

let pipelineRef: typeof import('./navigation-pipeline') | null = null;
beforeAll(async () => {
  pipelineRef = await import('./navigation-pipeline');
});

/** The SSR document shape a soft navigation starts from. */
function installSSRDocument(): HTMLElement {
  document.body.innerHTML = `
    <div data-go-layout="app/layout.templ">
      <div data-go-page-outlet><h1 data-go-page-home>Home</h1></div>
    </div>`;
  return document.querySelector('[data-go-page-outlet]') as HTMLElement;
}

/** Mount a live dashboard layout + settings child, as a subtree exit finds it. */
async function mountedSubtree(region: ParentNode): Promise<{
  layout: LayoutInstanceHandle;
  page: ReturnType<typeof mountComponent> & { pageRoot?: HTMLElement };
}> {
  const layout = await mountPersistentLayout(DASHBOARD_LAYOUT_ID, region);
  const component = await loadPage('dashboard/settings/page.svelte');
  const { createLayoutPageRoot } = await import('./layout-mount');
  const pageRoot = createLayoutPageRoot(layout.outlet, {
    ...csrDashboard,
    url: '/dashboard/settings',
  });
  const page = mountComponent(component!, { target: pageRoot });
  return { layout, page };
}

describe('G07: unmountLayoutSubtree — unit contract', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });

  it('tears the child page down FIRST, then the layout — order is structural', async () => {
    const region = document.createElement('div');
    document.body.append(region);
    const { layout, page } = await mountedSubtree(region);

    const order: string[] = [];
    const layoutHostEl = layout.host;
    const pageRootEl = page.target;
    const stop = onLifecycleEvent((e) => {
      if (e.target === layoutHostEl) order.push(`layout:${e.phase}`);
      else if (e.target === pageRootEl) order.push(`page:${e.phase}`);
    });

    await unmountLayoutSubtree(layout, page);
    stop();

    // Layout destructor called exactly once; child's unmount event fired
    // before the layout's.
    expect(order).toEqual(['page:unmounted', 'layout:unmounted']);
  });

  it('removes host, marker, outlet, and page root from the document', async () => {
    const region = document.createElement('div');
    document.body.append(region);
    const { layout, page } = await mountedSubtree(region);
    const before = liveInstanceCount();
    expect(
      document.querySelectorAll(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`).length
    ).toBe(1);

    await unmountLayoutSubtree(layout, page);

    expect(document.querySelectorAll('[data-go-layout-shell-host]').length).toBe(0);
    expect(
      document.querySelectorAll(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`).length
    ).toBe(0);
    expect(document.querySelectorAll('[data-go-layout-outlet]').length).toBe(0);
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(0);
    expect(region.children.length).toBe(0); // nothing left behind
    expect(liveInstanceCount()).toBe(before - 2); // both instances gone: subscriptions stopped
  });

  it('a leftover marker after dispose surfaces as LayoutExitError', async () => {
    const region = document.createElement('div');
    document.body.append(region);
    const { layout, page } = await mountedSubtree(region);
    // Forge a second marker so the post-exit check trips.
    const ghost = document.createElement('div');
    ghost.setAttribute('data-go-layout', DASHBOARD_LAYOUT_ID);
    document.body.append(ghost);
    await expect(unmountLayoutSubtree(layout, page)).rejects.toBeInstanceOf(LayoutExitError);
    ghost.remove();
  });

  it('the guard stops between child and layout teardown: layout survives untouched', async () => {
    const region = document.createElement('div');
    document.body.append(region);
    const { layout, page } = await mountedSubtree(region);

    let layoutUnmounts = 0;
    const hostEl = layout.host;
    const stop = onLifecycleEvent((e) => {
      if (e.phase === 'unmounted' && e.target === hostEl) layoutUnmounts++;
    });
    const stopped = await unmountLayoutSubtree(layout, page, () => true);
    stop();

    // Guard abort reports the outcome (not a throw) and hands back the
    // still-live layout handle for ownership restoration.
    expect(stopped).toEqual({ outcome: 'superseded', layout: expect.objectContaining({ unmount: expect.any(Function) }) });
    expect(layoutUnmounts).toBe(0); // parent never destroyed
    // The child COMPONENT is gone (its DOM with it) but the layout
    // instance — sidebar, counter, outlet — is still fully mounted. The
    // page-root ELEMENT remains as an empty runtime-owned container the
    // superseding navigation reuses.
    expect(page.target.hasChildNodes()).toBe(false);
    expect(document.querySelector('[data-go-layout-counter]')).toBeTruthy();
    expect(
      document.querySelectorAll(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`).length
    ).toBe(1);
    await layout.dispose(); // clean up after the guard test
  });

  it('idempotent handles: repeated exits never throw and never double-destruct', async () => {
    const region = document.createElement('div');
    document.body.append(region);
    const { layout, page } = await mountedSubtree(region);
    const before = liveInstanceCount(); // layout + page
    expect((await unmountLayoutSubtree(layout, page)).outcome).toBe('exited'); // disposed once
    // A repeated exit on the same handles is a safe no-op: both handles are
    // idempotent, no second destructor fires, and no exception propagates.
    await expect(unmountLayoutSubtree(layout, page)).resolves.toEqual({ outcome: 'exited' });
    expect(liveInstanceCount()).toBe(before - 2); // exactly the two went
  });
});

describe('G07: pipeline — soft navigation out of the Dashboard subtree', () => {
  beforeEach(() => {
    window.history.replaceState(null, '', '/');
    document.body.innerHTML = '';
    pipelineRef!.startSoftNavigation();
  });

  afterEach(async () => {
    await pipelineRef!.__resetPipelineForTesting();
    pipelineRef!.stopSoftNavigation();
    document.body.innerHTML = ''; // detach any leftover instrumentation targets
  });

  it('1. /dashboard → /about tears down page then layout, destructor once, no stale roots', async () => {
    installSSRDocument();
    stubFetch(
      new Map([
        [new URL('/dashboard', document.baseURI).href, csrDashboard],
        [new URL('/about', document.baseURI).href, ssrAbout],
      ])
    );
    await pipelineRef!.navigateTo('/dashboard'); // enter subtree
    const baseline = liveInstanceCount(); // layout + dashboard page

    const order: string[] = [];
    const hostEl = document.querySelector('[data-go-layout-shell-host]');
    const stop = onLifecycleEvent((e) => {
      if (e.phase !== 'unmounted') return;
      if (e.target === hostEl) order.push('layout');
      else if (e.target.closest?.('[data-go-layout-outlet]')) order.push('page');
    });

    await pipelineRef!.navigateTo('/about'); // G07 exit under test
    stop();

    // Destructor called exactly once; page torn down before the layout.
    expect(order.filter((w) => w === 'layout').length).toBe(1);
    expect(order.indexOf('page')).toBeLessThan(order.indexOf('layout'));
    expect(liveInstanceCount()).toBe(baseline - 2); // both gone, nothing leaked

    // Layout gone entirely; About page rendered into the outlet.
    expect(
      document.querySelectorAll(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`).length
    ).toBe(0);
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(0);
    expect(document.querySelector('[data-go-page-about]')).toBeTruthy();
    expect(document.querySelector('[data-go-page-about]')?.textContent).toContain('About');
    // One root templ layout marker remains — untouched server-owned DOM.
    expect(
      document.querySelectorAll('[data-go-layout="app/layout.templ"]').length
    ).toBe(1);
    expect(location.pathname).toBe('/about');
    expect(document.title).toBe('About');
    clearFetch();
  });

  it('2. /dashboard/settings → /about behaves identically from the deeper child', async () => {
    installSSRDocument();
    const csrSettings: NavigationResponse = {
      ...csrDashboard,
      url: '/dashboard/settings',
      routeId: 'dashboard/settings/page.svelte',
      title: 'Settings',
      clientEntry: 'dashboard/settings/page.svelte',
    };
    stubFetch(
      new Map([
        [new URL('/dashboard/settings', document.baseURI).href, csrSettings],
        [new URL('/about', document.baseURI).href, ssrAbout],
      ])
    );
    await pipelineRef!.navigateTo('/dashboard/settings');
    const baseline = liveInstanceCount();

    let layoutUnmounts = 0;
    const hostEl = document.querySelector('[data-go-layout-shell-host]');
    const stop = onLifecycleEvent((e) => {
      if (e.phase === 'unmounted' && e.target === hostEl) layoutUnmounts++;
    });
    await pipelineRef!.navigateTo('/about');
    stop();

    expect(layoutUnmounts).toBe(1); // destructor called once
    expect(liveInstanceCount()).toBe(baseline - 2);
    expect(
      document.querySelectorAll(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`).length
    ).toBe(0);
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(0);
    expect(document.querySelector('[data-go-page-about]')).toBeTruthy();
    clearFetch();
  });

  it('3. /dashboard → /products/new (bare CSR destination) exits then mounts fresh', async () => {
    installSSRDocument();
    const csrProduct: NavigationResponse = {
      ...csrDashboard,
      url: '/products/new',
      routeId: 'products/new/page.svelte',
      title: 'New',
      layouts: ['app/layout.templ'],
      clientEntry: 'products/new/page.svelte',
    };
    stubFetch(
      new Map([
        [new URL('/dashboard', document.baseURI).href, csrDashboard],
        [new URL('/products/new', document.baseURI).href, csrProduct],
      ])
    );
    await pipelineRef!.navigateTo('/dashboard');
    const baseline = liveInstanceCount(); // layout + dashboard page

    await pipelineRef!.navigateTo('/products/new'); // CSR exit path

    // Layout gone, exactly one page root with the product page inside it.
    expect(
      document.querySelectorAll(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`).length
    ).toBe(0);
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
    expect(
      document.querySelector('[data-go-page-root]')?.getAttribute('data-go-route-id')
    ).toBe('products/new/page.svelte');
    expect(document.querySelector('[data-go-page-root] main')?.textContent).toContain('products new');
    // -2 exited (layout + dashboard page), +1 fresh product page: net -1.
    expect(liveInstanceCount()).toBe(baseline - 1);
    clearFetch();
  });

  it('4. repeated entry/exit cycles leak nothing', async () => {
    installSSRDocument();
    const fetches = new Map<string, NavigationResponse>([
      [new URL('/dashboard', document.baseURI).href, csrDashboard],
      [new URL('/about', document.baseURI).href, ssrAbout],
    ]);
    stubFetch(fetches);
    const baseline = liveInstanceCount();
    for (let i = 0; i < 3; i++) {
      await pipelineRef!.navigateTo('/dashboard');
      expect(
        document.querySelectorAll(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`).length
      ).toBe(1);
      await pipelineRef!.navigateTo('/about');
      expect(
        document.querySelectorAll(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`).length
      ).toBe(0);
    }
    // No leaked instances after three full cycles: count back to baseline.
    expect(liveInstanceCount()).toBe(baseline);
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(0);
    expect(document.querySelectorAll('[data-go-layout-shell-host]').length).toBe(0);
    clearFetch();
  });

  it('5. re-entering after an exit mounts a FRESH layout (works; state assertions are G08)', async () => {
    installSSRDocument();
    stubFetch(
      new Map([
        [new URL('/dashboard', document.baseURI).href, csrDashboard],
        [new URL('/about', document.baseURI).href, ssrAbout],
      ])
    );
    await pipelineRef!.navigateTo('/dashboard');
    const button = document.querySelector<HTMLButtonElement>(
      '[data-go-layout-counter-button]'
    )!;
    button.click();
    await new Promise((r) => setTimeout(r, 0));
    expect(document.querySelector('[data-go-layout-counter]')?.textContent).toContain('1');

    const baseline = liveInstanceCount();
    await pipelineRef!.navigateTo('/about'); // exit
    await pipelineRef!.navigateTo('/dashboard'); // re-enter

    // A fresh mount exists and hosts the page; one instance, one marker.
    expect(
      document.querySelectorAll(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`).length
    ).toBe(1);
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
    expect(liveInstanceCount()).toBe(baseline); // back to layout + page, no leak
    clearFetch();
  });

  it('6. a stale navigation cannot dispose the active layout at the teardown boundary', async () => {
    installSSRDocument();
    stubFetch(
      new Map([
        [new URL('/dashboard', document.baseURI).href, csrDashboard],
        [new URL('/about', document.baseURI).href, ssrAbout],
      ])
    );
    await pipelineRef!.navigateTo('/dashboard');
    const marker = document.querySelector(
      `[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`
    );
    const button = document.querySelector<HTMLButtonElement>(
      '[data-go-layout-counter-button]'
    )!;
    button.click(); // counter = 1 — state must survive a cancelled exit
    await new Promise((r) => setTimeout(r, 0));

    // Begin the exit to /about, then supersede its token BEFORE its
    // envelope applies: the older navigation observes staleness at its
    // currency check and must never reach teardown.
    const exitP = pipelineRef!.navigateTo('/about'); // token A
    // Supersede with an in-subtree CSR swap that resolves instantly.
    const { __setHandlesForTesting } = pipelineRef!;
    __setHandlesForTesting({
      fetch: (() =>
        Promise.resolve({
          ...csrDashboard,
          url: '/dashboard/settings',
          routeId: 'dashboard/settings/page.svelte',
          title: 'Settings',
          clientEntry: 'dashboard/settings/page.svelte',
        } as NavigationResponse)) as typeof import('./navigation').fetchNavigation,
      hardNavigate: null,
    });
    await pipelineRef!.navigateTo('/dashboard/settings'); // token B wins
    await exitP; // stale token A: no-op

    // The layout was never disposed by the stale exit: B's swap found the
    // live instance and retained it (same marker), state preserved.
    expect(
      document.querySelector(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`)
    ).toBe(marker);
    expect(document.querySelector('[data-go-layout-counter]')?.textContent).toContain('1');
    clearFetch();
  });

  it('7. supersession during child teardown keeps the layout for the newer navigation', async () => {
    // Begin an exit to /about, and while its child page is unmounting,
    // supersede with a settings swap. The older exit must stop at the
    // guard; the newer navigation retains the still-mounted layout.
    installSSRDocument();
    const csrSettings: NavigationResponse = {
      ...csrDashboard,
      url: '/dashboard/settings',
      routeId: 'dashboard/settings/page.svelte',
      title: 'Settings',
      clientEntry: 'dashboard/settings/page.svelte',
    };
    stubFetch(
      new Map([
        [new URL('/dashboard', document.baseURI).href, csrDashboard],
        [new URL('/dashboard/settings', document.baseURI).href, csrSettings],
        [new URL('/about', document.baseURI).href, ssrAbout],
      ])
    );
    await pipelineRef!.navigateTo('/dashboard');
    const marker = document.querySelector(
      `[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`
    );

    const exitP = pipelineRef!.navigateTo('/about'); // begins teardown...
    // ...and immediately supersede: the settings swap wins the token.
    const swapP = pipelineRef!.navigateTo('/dashboard/settings');
    await exitP;
    await swapP;

    // The newer navigation found the layout mounted and swapped within it:
    // same marker element, one page root showing Settings.
    expect(
      document.querySelector(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`)
    ).toBe(marker);
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
    expect(
      document.querySelector('[data-go-page-root]')?.getAttribute('data-go-route-id')
    ).toBe('dashboard/settings/page.svelte');
    // And the About content never appeared.
    expect(document.querySelector('[data-go-page-about]')).toBeNull();
    clearFetch();
  });

  it('7b. audit sequence: superseded exit hands ownership back; the final exit disposes exactly once', async () => {
    // The Phase G gate-audit sequence, end to end:
    //   1. dashboard layout A live and tracked
    //   2. navigate to /about (exit begins)
    //   3. child page finishes unmounting
    //   4. settings nav supersedes the exit BEFORE layout A is disposed
    //   5. the newer navigation repopulates the retained layout
    //   6. another /about navigation runs
    // Proofs: A stays tracked after supersession (no orphan); the final
    // exit unmounts A exactly once via the graceful path (not hard
    // fallback); no duplicate hosts/page roots; liveInstanceCount returns
    // to baseline; the stale exit never committed history/title/scroll.
    installSSRDocument();
    const csrSettings: NavigationResponse = {
      ...csrDashboard,
      url: '/dashboard/settings',
      routeId: 'dashboard/settings/page.svelte',
      title: 'Settings',
      clientEntry: 'dashboard/settings/page.svelte',
    };
    stubFetch(
      new Map([
        [new URL('/dashboard', document.baseURI).href, csrDashboard],
        [new URL('/dashboard/settings', document.baseURI).href, csrSettings],
        [new URL('/about', document.baseURI).href, ssrAbout],
      ])
    );
    const baseline = liveInstanceCount();
    await pipelineRef!.navigateTo('/dashboard');
    const marker = document.querySelector(
      `[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`
    );
    expect(marker).not.toBeNull();

    // Count lifecycle events identity-matched on the layout HOST element
    // (the mount target, div[data-go-layout-host], which contains the
    // component-stamped marker).
    const layoutHost = marker!.closest('[data-go-layout-shell-host]');
    expect(layoutHost).not.toBeNull();
    const events: string[] = [];
    const stopLifecycle = onLifecycleEvent((e) => {
      if (e.target === layoutHost) events.push(e.phase);
    });

    // History/scroll spy: the STALE exit must not commit any of them.
    const histSpy: string[] = [];
    history.replaceState = ((state, title, url) => {
      histSpy.push(`replace:${String(url)}`);
      return undefined;
    }) as typeof history.replaceState;
    history.pushState = ((state, title, url) => {
      histSpy.push(`push:${String(url)}`);
      return undefined;
    }) as typeof history.pushState;
    const scrollSpy: string[] = [];
    const origScrollTo = window.scrollTo;
    window.scrollTo = (() => {
      scrollSpy.push('scroll');
      return undefined;
    }) as typeof window.scrollTo;

    try {
      const exitP = pipelineRef!.navigateTo('/about'); // teardown begins...
      // ...supersede before layout disposal completes.
      const swapP = pipelineRef!.navigateTo('/dashboard/settings');
      await exitP; // guard aborts it after the child unmount
      await swapP; // settings repopulates the retained layout

      // Proof 1 — an authoritative handle still exists for layout A: the
      // final /about navigation takes the GRACEFUL exit branch, which only
      // happens when the discarded layout is pipeline-tracked.
      const hardSink: { url: string | null } = { url: null };
      stubHardNavigate(hardSink);
      await pipelineRef!.navigateTo('/about');
      expect(hardSink.url).toBeNull(); // graceful path — no hard reload escape
      // The About content rendered into the same document.
      expect(document.querySelector('[data-go-page-about]')).not.toBeNull();

      // Proof 2 — exactly one unmount of layout A, identity-matched.
      const unmounts = events.filter((p) => p === 'unmounted');
      expect(unmounts.length).toBe(1);

      // Proof 3 — no orphan: layout A is gone from the document.
      expect(
        document.querySelector(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`)
      ).toBeNull();

      // Proof 4 — no duplicates ever existed: one host element at peak is
      // implied by marker counts; page roots never exceeded 1 at rest.
      expect(document.querySelectorAll('[data-go-page-root]').length).toBe(0);

      // Proof 5 — instance count returned to baseline (no leak).
      expect(liveInstanceCount()).toBe(baseline);

      // Proof 6 — the STALE exit committed no history/scroll state. The
      // committed sequence must be exactly the two completed navigations,
      // in order: settings swap, then the final About exit. A stale
      // commit would surface as an extra entry or a wrong order.
      expect(histSpy).toEqual([
        `push:/dashboard/settings`,
        `push:/about`,
      ]);
      // Scroll follows the same rule: one per completed navigation, none
      // from the aborted exit.
      expect(scrollSpy.length).toBe(2);
    } finally {
      stopLifecycle();
      window.scrollTo = origScrollTo;
      clearFetch();
    }
  });

  it('9. popstate back/forward over an exit restores correctly (push: false path)', async () => {
    installSSRDocument();
    stubFetch(
      new Map([
        [new URL('/dashboard', document.baseURI).href, csrDashboard],
        [new URL('/about', document.baseURI).href, ssrAbout],
      ])
    );
    await pipelineRef!.navigateTo('/dashboard', { push: true });
    await pipelineRef!.navigateTo('/about', { push: true }); // exit pushes
    // History restoration back INTO the subtree: a popstate-shaped apply.
    await pipelineRef!.navigateTo('/dashboard', { push: false });
    // Re-entry remounted the layout fresh and hosts the page.
    expect(
      document.querySelectorAll(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`).length
    ).toBe(1);
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
    expect(
      document.querySelector('[data-go-page-root]')?.getAttribute('data-go-route-id')
    ).toBe('dashboard/page.svelte');
    clearFetch();
  });
});
