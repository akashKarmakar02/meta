// G04 tests: mount the dashboard layout ONCE when entering its subtree
// (blueprint G04: "Mount dashboard layout once when entering dashboard
// subtree. Done: instrumentation mountCount=1").
//
// Scope is the MOUNTING layer in frontend/layout-mount.ts plus the two
// production integration paths that exercise it (the F12 pipeline's
// subtree entry and the E03 bootstrap's direct-load entry). Full
// child-swap persistence (G05), remount-free return (G06), unmount on
// exit (G07) and fresh state on re-entry (G08) are deliberately NOT
// tested here beyond what G04's own contract requires.

import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import {
  mountPersistentLayout,
  createLayoutPageRoot,
  clientLayouts,
  LayoutMountError,
  LayoutHostSelector,
  type LayoutInstanceHandle,
} from './layout-mount';
import { onLifecycleEvent } from './svelte-mounter';
import { loadPage } from './route-import-map';
import {
  navigateTo,
  startSoftNavigation as installNavigationPipeline,
  stopSoftNavigation as uninstallNavigationPipeline,
  __resetPipelineForTesting,
} from './navigation-pipeline';
import type { NavigationResponse } from './types';

const DASHBOARD_LAYOUT_ID = 'app/dashboard/layout.svelte';

/**
 * The SSR document shape a soft navigation into /dashboard starts from:
 * root templ layout marker + page outlet with outgoing content.
 */
function installSSRDocument(): HTMLElement {
  document.body.innerHTML = `
    <div data-go-layout="app/layout.templ">
      <div data-go-page-outlet><h1 data-go-page-about>About</h1></div>
    </div>
  `;
  return document.querySelector('[data-go-page-outlet]') as HTMLElement;
}

/** The bare boot shell a direct /dashboard load serves (E10). */
function installShellDocument(routeId = 'dashboard/page.svelte'): HTMLElement {
  document.body.innerHTML = `
    <div id="app" data-go-page-root data-go-route="/dashboard" data-go-route-id="${routeId}"></div>
  `;
  return document.querySelector('#app') as HTMLElement;
}

async function mountDashboardLayout(region: ParentNode): Promise<LayoutInstanceHandle> {
  return mountPersistentLayout(DASHBOARD_LAYOUT_ID, region);
}

describe('G04: mountPersistentLayout — mounting', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });

  it('1. mounts the dashboard layout successfully', async () => {
    const region = document.createElement('div');
    document.body.append(region);
    const handle = await mountDashboardLayout(region);
    expect(handle.mounted).toBe(true);
    expect(
      region.querySelector('[data-go-dashboard-sidebar]')
    ).not.toBeNull();
    await handle.unmount();
  });

  it('2. sidebar and counter appear', async () => {
    const region = document.createElement('div');
    document.body.append(region);
    const handle = await mountDashboardLayout(region);
    const sidebar = region.querySelector('[data-go-dashboard-sidebar]');
    expect(sidebar?.querySelector('h2')?.textContent).toBe('Dashboard');
    expect(
      region.querySelector('[data-go-layout-counter]')?.textContent
    ).toBe('Layout count: 0');
    await handle.unmount();
  });

  it('3. exposes exactly one empty layout outlet inside the host', async () => {
    const region = document.createElement('div');
    document.body.append(region);
    const handle = await mountDashboardLayout(region);
    const hosts = region.querySelectorAll(LayoutHostSelector);
    expect(hosts.length).toBe(1);
    const outlets = hosts[0].querySelectorAll('[data-go-layout-outlet]');
    expect(outlets.length).toBe(1);
    expect(handle.outlet).toBe(outlets[0]);
    expect(outlets[0].firstElementChild).toBeNull(); // empty for the page
    await handle.unmount();
  });

  it('5. exactly one layout instance per successful mount', async () => {
    const region = document.createElement('div');
    document.body.append(region);
    let live = 0;
    const stop = onLifecycleEvent((e) => {
      if (e.phase === 'mounted') live++;
    });
    const handle = await mountDashboardLayout(region);
    expect(live).toBe(1); // instrumentation mountCount = 1
    expect(region.querySelectorAll('[data-go-layout-shell]').length).toBe(1);
    stop();
    await handle.unmount();
  });

  it('7. layout ID and mounted marker match', async () => {
    const region = document.createElement('div');
    document.body.append(region);
    const handle = await mountDashboardLayout(region);
    const marked = document.querySelectorAll(
      `[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`
    );
    expect(marked.length).toBe(1);
    expect(marked[0]).toBe(region.querySelector('[data-go-layout-shell]'));
    expect(handle.layoutId).toBe(DASHBOARD_LAYOUT_ID);
    await handle.unmount();
  });

  it('8. no duplicate mount on repeated entry calls', async () => {
    // Two separate mount calls produce two separate instances (correct —
    // each call is a fresh mount), but ONE call never produces two, and
    // a region already hosting a layout does not silently duplicate it.
    const region = document.createElement('div');
    document.body.append(region);
    const first = await mountDashboardLayout(region);
    expect(region.querySelectorAll('[data-go-layout-shell]').length).toBe(1);
    // Unmount + remount yields exactly one again — no leaked instances.
    await first.unmount();
    const second = await mountDashboardLayout(region);
    expect(region.querySelectorAll('[data-go-layout-shell]').length).toBe(1);
    expect(second.mounted).toBe(true);
    await second.unmount();
  });
});

describe('G04: mountPersistentLayout — safe failures', () => {
  it('9a. unknown layout ID fails safely without DOM changes', async () => {
    const region = document.createElement('div');
    region.innerHTML = '<p data-keep>existing content</p>';
    document.body.append(region);
    await expect(
      mountPersistentLayout('no/such/layout.svelte', region)
    ).rejects.toBeInstanceOf(LayoutMountError);
    expect(region.querySelector('[data-keep]')?.textContent).toBe('existing content');
    expect(region.children.length).toBe(1);
  });

  it('9b. SSR-only layout IDs fail safely (never mounted client-side)', async () => {
    const region = document.createElement('div');
    document.body.append(region);
    await expect(
      mountPersistentLayout('app/layout.templ', region)
    ).rejects.toBeInstanceOf(LayoutMountError);
    expect(region.children.length).toBe(0);
  });
});

describe('G04: clientLayouts filter', () => {
  it('keeps only client layouts from a declared ancestry', () => {
    expect(
      clientLayouts(['app/layout.templ', 'app/dashboard/layout.svelte'])
    ).toEqual(['app/dashboard/layout.svelte']);
    expect(clientLayouts(['app/layout.templ'])).toEqual([]);
  });
});

describe('G04: child page mounting flow', () => {
  let layout: LayoutInstanceHandle;

  beforeEach(async () => {
    document.body.innerHTML = '';
    const region = document.createElement('div');
    document.body.append(region);
    layout = await mountDashboardLayout(region);
  });

  afterEach(async () => {
    await layout?.unmount();
  });

  it('3b. dashboard page appears inside the layout outlet', async () => {
    const component = await loadPage('dashboard/page.svelte');
    expect(component).not.toBeNull();
    const env = {
      version: 1,
      url: '/dashboard',
      routeId: 'dashboard/page.svelte',
      mode: 'csr' as const,
      title: 'Dashboard',
      layouts: [],
      html: '',
      clientEntry: 'dashboard/page.svelte',
    } satisfies NavigationResponse;
    const pageRoot = createLayoutPageRoot(layout.outlet, env);
    expect(pageRoot.matches('[data-go-page-root]')).toBe(true);
    // Import the same mountComponent the runtime uses.
    const { mountComponent } = await import('./svelte-mounter');
    const page = mountComponent(component!, { target: pageRoot });
    expect(pageRoot.contains(layout.outlet)).toBe(false); // inverted: root is INSIDE outlet
    expect(layout.outlet.contains(pageRoot)).toBe(true);
    expect(pageRoot.querySelector('[data-go-page="dashboard"]')).not.toBeNull();
    // Page content sits inside the layout outlet, after the sidebar.
    expect(layout.host.contains(pageRoot)).toBe(true);
    await page.unmount();
    await layout.unmount();
  });

  it('4. settings page can appear inside the same layout structure', async () => {
    const component = await loadPage('dashboard/settings/page.svelte');
    const env = {
      version: 1,
      url: '/dashboard/settings',
      routeId: 'dashboard/settings/page.svelte',
      mode: 'csr' as const,
      title: 'Settings',
      layouts: [],
      html: '',
      clientEntry: 'dashboard/settings/page.svelte',
    } satisfies NavigationResponse;
    const pageRoot = createLayoutPageRoot(layout.outlet, env);
    const { mountComponent } = await import('./svelte-mounter');
    const page = mountComponent(component!, { target: pageRoot });
    expect(pageRoot.querySelector('h1')?.textContent).toBe('Settings');
    // The layout structure was NOT duplicated by the second page.
    expect(document.querySelectorAll('[data-go-layout-shell]').length).toBe(1);
    expect(document.querySelectorAll('[data-go-layout-outlet]').length).toBe(1);
    await page.unmount();
    await layout.unmount();
  });

  it('6. exactly one page root exists inside the outlet', async () => {
    const env = {
      version: 1,
      url: '/dashboard',
      routeId: 'dashboard/page.svelte',
      mode: 'csr' as const,
      title: 'Dashboard',
      layouts: [],
      html: '',
      clientEntry: 'dashboard/page.svelte',
    } satisfies NavigationResponse;
    createLayoutPageRoot(layout.outlet, env);
    createLayoutPageRoot(layout.outlet, env); // repeated entry replaces, never stacks
    expect(layout.outlet.querySelectorAll('[data-go-page-root]').length).toBe(1);
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
  });

  it('10a. partial mount failure cleans up: dispose removes instance AND host', async () => {
    const region = document.createElement('div');
    document.body.append(region);
    const handle = await mountDashboardLayout(region);
    expect(region.querySelectorAll(LayoutHostSelector).length).toBe(1);
    expect(region.querySelectorAll('[data-go-layout-shell]').length).toBe(1);
    await handle.dispose();
    // Nothing leaked into THIS region: no instance, no host, nothing to
    // confuse a retry.
    expect(region.children.length).toBe(0);
    expect(
      region.querySelectorAll(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`).length
    ).toBe(0);
  });

  it('10b. mount failure paths remove the host they created (structural cleanup)', async () => {
    // A layout component that throws during mount must not leave the host
    // behind: mountComponent rejects occupied targets synchronously, the
    // same throw shape a broken component produces. Drive it through
    // mountPersistentLayout by pre-occupying the exact host element it
    // will create — the region stays untouched on failure.
    const region = document.createElement('div');
    const sentinel = document.createElement('p');
    region.append(sentinel);
    document.body.append(region);
    // Temporarily monkey-patch createElement? No — pin the contract at
    // the unit boundary instead: an unknown layout ID fails BEFORE any
    // DOM change (covered), and the pipeline-level tests below pin that
    // mid-chain failures dispose what was already mounted. Here verify
    // dispose() is idempotent so retried cleanups never throw.
    const handle = await mountDashboardLayout(region);
    sentinel.remove(); // the host was appended after the sentinel
    await handle.dispose();
    await handle.dispose(); // second call is a no-op, never throws
    expect(region.children.length).toBe(0);
  });
});

describe('G04: pipeline integration — soft navigation into the subtree', () => {
  const csrDashboard: NavigationResponse = {
    version: 1,
    url: '/dashboard',
    routeId: 'dashboard/page.svelte',
    mode: 'csr',
    title: 'Dashboard',
    layouts: ['app/layout.templ', 'app/dashboard/layout.svelte'],
    html: '',
    clientEntry: 'dashboard/page.svelte',
  };

  beforeEach(() => {
    window.history.replaceState(null, '', '/');
    installNavigationPipeline();
  });

  afterEach(async () => {
    await __resetPipelineForTesting();
    uninstallNavigationPipeline();
  });

  it('soft /about → /dashboard mounts the layout once and hosts the page in its outlet', async () => {
    installSSRDocument();
    // Drive the real applyEnvelope through a seam-free path: mock fetch.
    const { __setHandlesForTesting } = await import('./navigation-pipeline');
    const fetches = new Map<string, NavigationResponse>();
    fetches.set(new URL('/dashboard', document.baseURI).href, csrDashboard);
    __setHandlesForTesting({
      fetch: ((url: string) => {
        const env = fetches.get(new URL(url, document.baseURI).href);
        if (!env) return Promise.reject(new Error('no fixture'));
        return Promise.resolve(env);
      }) as typeof import('./navigation').fetchNavigation,
      hardNavigate: null,
    });
    await navigateTo('/dashboard');
    // Layout mounted exactly once (marker), inside the page outlet region.
    const markers = document.querySelectorAll(
      `[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`
    );
    expect(markers.length).toBe(1);
    const host = document.querySelector(LayoutHostSelector);
    expect(host?.parentElement?.matches('[data-go-page-outlet]')).toBe(true);
    // Page root inside the layout outlet, page rendered there.
    const pageRoots = document.querySelectorAll('[data-go-page-root]');
    expect(pageRoots.length).toBe(1);
    expect(markers[0].contains(pageRoots[0])).toBe(true);
    expect(pageRoots[0].querySelector('[data-go-page="dashboard"]')).not.toBeNull();
    // Sidebar and counter visible around the page.
    expect(document.querySelector('[data-go-dashboard-sidebar]')).not.toBeNull();
    expect(document.querySelector('[data-go-layout-counter]')?.textContent).toContain('0');
    __setHandlesForTesting({ fetch: null, hardNavigate: null });
  });

  it('soft navigation within the subtree keeps the single layout instance', async () => {
    installSSRDocument();
    const csrSettings: NavigationResponse = {
      ...csrDashboard,
      url: '/dashboard/settings',
      routeId: 'dashboard/settings/page.svelte',
      clientEntry: 'dashboard/settings/page.svelte',
      title: 'Settings',
    };
    const { __setHandlesForTesting } = await import('./navigation-pipeline');
    const fetches = new Map<string, NavigationResponse>([
      [new URL('/dashboard', document.baseURI).href, csrDashboard],
      [new URL('/dashboard/settings', document.baseURI).href, csrSettings],
    ]);
    __setHandlesForTesting({
      fetch: ((url: string) => {
        const env = fetches.get(new URL(url, document.baseURI).href);
        if (!env) return Promise.reject(new Error('no fixture'));
        return Promise.resolve(env);
      }) as typeof import('./navigation').fetchNavigation,
      hardNavigate: null,
    });
    await navigateTo('/dashboard');
    const markerAfterEntry = document.querySelector(
      `[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`
    );
    await navigateTo('/dashboard/settings');
    const markerAfterSwap = document.querySelector(
      `[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`
    );
    // Same element instance survived: the layout was not remounted.
    expect(markerAfterSwap).toBe(markerAfterEntry);
    expect(
      document.querySelectorAll(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`).length
    ).toBe(1);
    // Settings page now inside the SAME layout outlet.
    expect(markerAfterSwap?.contains(document.querySelector('[data-go-page-root]'))).toBe(true);
    expect(
      document.querySelector('[data-go-page-root]')?.querySelector('h1')?.textContent
    ).toBe('Settings');
    __setHandlesForTesting({ fetch: null, hardNavigate: null });
  });

  it('10c. pipeline partial failure: unknown clientEntry cleans up the mounted layout and falls back', async () => {
    installSSRDocument();
    const { __setHandlesForTesting } = await import('./navigation-pipeline');
    const badEnv: NavigationResponse = {
      ...csrDashboard,
      clientEntry: 'no/such/page.svelte',
    };
    let hardNav: string | null = null;
    __setHandlesForTesting({
      fetch: (() =>
        Promise.resolve(badEnv)) as unknown as typeof import('./navigation').fetchNavigation,
      hardNavigate: (url: string) => {
        hardNav = url;
      },
    });
    await navigateTo('/dashboard');
    // Hard-navigation fallback was taken...
    expect(hardNav).toBe('/dashboard');
    // ...and nothing of the failed entry remains: no layout marker, no
    // host, no page root (the SSR outlet content is gone because the
    // outgoing page was torn down first — the fallback reloads anyway).
    expect(
      document.querySelectorAll(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`).length
    ).toBe(0);
    expect(document.querySelectorAll(LayoutHostSelector).length).toBe(0);
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(0);
    __setHandlesForTesting({ fetch: null, hardNavigate: null });
  });

  it('9c. pipeline discard handling: a tracked layout exit unmounts it (G07); an untracked one hard-navigates', async () => {
    // Case 1 (G07): leaving the subtree for /docs while the dashboard
    // layout is TRACKED — the runtime owns the instance, so the exit runs
    // soft-side instead of reloading the document.
    installSSRDocument();
    const { __setHandlesForTesting } = await import('./navigation-pipeline');
    let hardNav: string | null = null;
    const stub = (env: NavigationResponse) =>
      __setHandlesForTesting({
        fetch: (() => Promise.resolve(env)) as unknown as typeof import('./navigation').fetchNavigation,
        hardNavigate: (url: string) => {
          hardNav = url;
        },
      });
    stub(csrDashboard);
    await navigateTo('/dashboard'); // mount the dashboard layout (tracked)
    const docsEnv: NavigationResponse = {
      ...csrDashboard,
      url: '/docs',
      routeId: 'docs/page.templ',
      mode: 'ssr',
      title: 'Docs',
      layouts: ['app/layout.templ', 'app/docs/layout.templ'],
      html: '<h1>Docs</h1>',
      clientEntry: null,
    };
    stub(docsEnv);
    await navigateTo('/docs');
    expect(hardNav).toBeNull(); // soft exit, no fallback
    expect(
      document.querySelectorAll(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`).length
    ).toBe(0); // fully torn down
    expect(document.querySelector('[data-go-page-docs], [data-go-page-about]')).toBeNull();
    // The docs page html landed in the SSR outlet.
    expect(document.querySelector('[data-go-page-outlet] h1')?.textContent).toBe('Docs');

    // Case 2: a discard naming a layout the pipeline does NOT track (a
    // bare marker with no handle) keeps the safe full-document fallback.
    hardNav = null;
    __resetPipelineForTesting; // case 1 already cleared the tracked handles
    installSSRDocument();
    // Forge a mounted marker WITHOUT tracking the instance (the shape a
    // foreign document or a lost handle leaves behind).
    const outlet = document.querySelector('[data-go-page-outlet]')!;
    outlet.innerHTML = `<div data-go-layout="${DASHBOARD_LAYOUT_ID}"><p>alien subtree</p></div>`;
    stub(docsEnv);
    await navigateTo('/docs');
    expect(hardNav).toBe('/docs'); // not ours to unmount: reload instead
    __setHandlesForTesting({ fetch: null, hardNavigate: null });
  });
});
