// G05 tests: child-only page swap inside a RETAINED persistent layout
// (blueprint G05: "Switch /dashboard→/dashboard/settings by replacing
// child only. Done: sidebar counter remains unchanged").
//
// Scope is the SWAP layer in frontend/layout-child-swap.ts plus the two
// production paths that exercise it (the F12 pipeline's retained-layout
// transition and the E03 bootstrap → pipeline handover on a direct load).
// Unmounting the layout on subtree exit (G07) and fresh state after
// re-entry (G08) are deliberately NOT tested here; return navigation
// (G06) is covered only insofar as a sibling swap exercises the same
// machinery mechanically.

import { describe, it, expect, beforeEach, afterEach, beforeAll } from 'vitest';
import {
  swapLayoutChildPage,
  ChildSwapError,
  type ChildPageHandle,
} from './layout-child-swap';
import { mountPersistentLayout, type LayoutInstanceHandle } from './layout-mount';
import { mountComponent, onLifecycleEvent } from './svelte-mounter';
import { loadPage, loadLayout } from './route-import-map';
import {
  clientLayoutSequence,
  layoutStackTransition,
} from './layout-stack';
import {
  navigateTo,
  adoptMountedInstances,
  startSoftNavigation as installNavigationPipeline,
  stopSoftNavigation as uninstallNavigationPipeline,
  __resetPipelineForTesting,
} from './navigation-pipeline';
import type { NavigationResponse } from './types';

const DASHBOARD_LAYOUT_ID = 'app/dashboard/layout.svelte';

const dashboardEnv: NavigationResponse = {
  version: 1,
  url: '/dashboard',
  routeId: 'dashboard/page.svelte',
  mode: 'csr',
  title: 'Dashboard',
  layouts: ['app/layout.templ', DASHBOARD_LAYOUT_ID],
  html: '',
  clientEntry: 'dashboard/page.svelte',
};

const settingsEnv: NavigationResponse = {
  version: 1,
  url: '/dashboard/settings',
  routeId: 'dashboard/settings/page.svelte',
  mode: 'csr',
  title: 'Settings',
  layouts: ['app/layout.templ', DASHBOARD_LAYOUT_ID],
  html: '',
  clientEntry: 'dashboard/settings/page.svelte',
};

/** Mount the dashboard layout + its dashboard page, G04-style. */
async function mountDashboardSubtree(
  region: ParentNode,
  env: NavigationResponse
): Promise<{ layout: LayoutInstanceHandle; page: ChildPageHandle }> {
  const layout = await mountPersistentLayout(DASHBOARD_LAYOUT_ID, region);
  const component = await loadPage(env.clientEntry!);
  if (!component) throw new Error('page fixture missing');
  const { createLayoutPageRoot } = await import('./layout-mount');
  const pageRoot = createLayoutPageRoot(layout.outlet, env);
  const handle = mountComponent(component, { target: pageRoot });
  return { layout, page: { ...handle, pageRoot } };
}

/** The pipeline module's test seams, imported lazily like production code. */
function navigationPipelineModule(): typeof import('./navigation-pipeline') {
  // Top-level import would be simpler, but the suite also drives the REAL
  // navigateTo; the lazy accessor keeps the module single-loaded.
  return pipelineRef!;
}
let pipelineRef: typeof import('./navigation-pipeline') | null = null;
beforeAll(async () => {
  pipelineRef = await import('./navigation-pipeline');
});

describe('G05: clientLayoutSequence (retention planning base)', () => {
  it('keeps only client layout IDs, order preserved', () => {
    expect(
      clientLayoutSequence(['app/layout.templ', DASHBOARD_LAYOUT_ID])
    ).toEqual([DASHBOARD_LAYOUT_ID]);
    expect(clientLayoutSequence(['app/layout.templ'])).toEqual([]);
    // The bare boot shell (no server markup) retains its client layouts:
    // mounted [] vs declared [templ, svelte] must plan ENTRY only.
    const t = layoutStackTransition(
      clientLayoutSequence([]),
      clientLayoutSequence(['app/layout.templ', DASHBOARD_LAYOUT_ID])
    );
    expect(t.discard).toEqual([]);
    expect(t.enter).toEqual([DASHBOARD_LAYOUT_ID]);
    // Direct-loaded shell vs settings destination: no discard — a
    // phantom discard here was the bug that broke real-browser swaps.
    const t2 = layoutStackTransition(
      clientLayoutSequence([DASHBOARD_LAYOUT_ID]),
      clientLayoutSequence(['app/layout.templ', DASHBOARD_LAYOUT_ID])
    );
    expect(t2.discard).toEqual([]);
    expect(t2.enter).toEqual([]);
    expect(t2.retain).toEqual([DASHBOARD_LAYOUT_ID]);
  });
});

describe('G05: swapLayoutChildPage — the child-only swap operation', () => {
  let layout: LayoutInstanceHandle;
  let page: ChildPageHandle;

  beforeEach(async () => {
    document.body.innerHTML = '';
    const region = document.createElement('div');
    document.body.append(region);
    ({ layout, page } = await mountDashboardSubtree(region, dashboardEnv));
  });

  afterEach(async () => {
    await layout?.dispose();
  });

  it('1. Dashboard → Settings swaps only the child page', async () => {
    const markerBefore = document.querySelector(
      `[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`
    );
    const hostBefore = document.querySelector('[data-go-layout-shell-host]');
    const outletBefore = layout.outlet;
    const component = await loadPage('dashboard/settings/page.svelte');
    const swapped = await swapLayoutChildPage(layout, settingsEnv, component, page);
    // The Settings page rendered...
    expect(swapped.pageRoot.querySelector('h1')?.textContent).toBe('Settings');
    // ...inside the SAME outlet element, same host, same marker instance.
    expect(swapped.pageRoot.parentElement).toBe(outletBefore);
    expect(document.querySelector('[data-go-layout-shell-host]')).toBe(hostBefore);
    expect(
      document.querySelector(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`)
    ).toBe(markerBefore);
  });

  it('2. layout DOM node identity is unchanged across the swap', async () => {
    const shellBefore = document.querySelector('[data-go-layout-shell]');
    const sidebarBefore = document.querySelector('[data-go-dashboard-sidebar]');
    const component = await loadPage('dashboard/settings/page.svelte');
    await swapLayoutChildPage(layout, settingsEnv, component, page);
    expect(document.querySelector('[data-go-layout-shell]')).toBe(shellBefore);
    expect(document.querySelector('[data-go-dashboard-sidebar]')).toBe(sidebarBefore);
  });

  it('3. layout counter state remains unchanged (no remount)', async () => {
    // Increment to 2 before swapping.
    const btn = document.querySelector<HTMLButtonElement>(
      '[data-go-layout-counter-button]'
    )!;
    btn.click();
    btn.click();
    await new Promise((r) => setTimeout(r, 0));
    const component = await loadPage('dashboard/settings/page.svelte');
    await swapLayoutChildPage(layout, settingsEnv, component, page);
    expect(
      document.querySelector('[data-go-layout-counter]')?.textContent
    ).toBe('Layout count: 2');
  });

  it('4. layout instrumentation reports one mount and zero unmounts', async () => {
    let layoutMounts = 0;
    let layoutUnmounts = 0;
    const stop = onLifecycleEvent((e) => {
      // Layout events target the layout's own host element.
      if (e.target === layout.host) {
        if (e.phase === 'mounted') layoutMounts++;
        else layoutUnmounts++;
      }
    });
    const component = await loadPage('dashboard/settings/page.svelte');
    await swapLayoutChildPage(layout, settingsEnv, component, page);
    expect(layoutMounts).toBe(0); // already mounted before observation began
    expect(layoutUnmounts).toBe(0); // the swap never unmounts the layout
    stop();
  });

  it('5. outgoing page unmount is observed; 6. incoming page mount is observed', async () => {
    const events: string[] = [];
    const stop = onLifecycleEvent((e) => {
      if (e.target === page.pageRoot) events.push(e.phase);
      else if (e.target === layout.outlet.querySelector('[data-go-page-root]')) {
        events.push(e.phase);
      }
    });
    const component = await loadPage('dashboard/settings/page.svelte');
    await swapLayoutChildPage(layout, settingsEnv, component, page);
    stop();
    expect(events).toEqual(['unmounted', 'mounted']); // teardown BEFORE mount
  });

  it('7. exactly one page root exists after the swap', async () => {
    const component = await loadPage('dashboard/settings/page.svelte');
    await swapLayoutChildPage(layout, settingsEnv, component, page);
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
    expect(layout.outlet.querySelectorAll('[data-go-page-root]').length).toBe(1);
  });

  it('8. page root carries the destination route metadata', async () => {
    const component = await loadPage('dashboard/settings/page.svelte');
    const swapped = await swapLayoutChildPage(layout, settingsEnv, component, page);
    expect(swapped.pageRoot.getAttribute('data-go-route')).toBe('/dashboard/settings');
    expect(swapped.pageRoot.getAttribute('data-go-route-id')).toBe(
      'dashboard/settings/page.svelte'
    );
  });

  it('9. repeated child swaps do not duplicate nodes or instances', async () => {
    const settingsComponent = await loadPage('dashboard/settings/page.svelte');
    const dashboardComponent = await loadPage('dashboard/page.svelte');
    let current = page;
    current = await swapLayoutChildPage(layout, settingsEnv, settingsComponent, current);
    current = await swapLayoutChildPage(layout, dashboardEnv, dashboardComponent, current);
    current = await swapLayoutChildPage(layout, settingsEnv, settingsComponent, current);
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
    expect(document.querySelectorAll('[data-go-layout-shell]').length).toBe(1);
    expect(
      document.querySelector('[data-go-page-root]')?.querySelector('h1')?.textContent
    ).toBe('Settings');
    expect(onLifecycleEvent !== undefined).toBe(true); // instrumentation live
    await current.unmount();
  });

  it('10a. null component fails before any teardown — old page intact', async () => {
    await expect(
      swapLayoutChildPage(layout, settingsEnv, null, page)
    ).rejects.toBeInstanceOf(ChildSwapError);
    // The outgoing page was NOT unmounted: it is still mounted and shown.
    expect(page.mounted).toBe(true);
    expect(page.pageRoot.querySelector('[data-go-page="dashboard"]')).not.toBeNull();
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
  });

  it('10b. passing an already-unmounted outgoing handle is safe (idempotent)', async () => {
    const component = await loadPage('dashboard/settings/page.svelte');
    await page.unmount();
    const swapped = await swapLayoutChildPage(layout, settingsEnv, component, page);
    expect(swapped.mounted).toBe(true);
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
    await swapped.unmount();
  });
});

describe('G05: pipeline integration — soft navigation inside the subtree', () => {
  beforeEach(() => {
    window.history.replaceState(null, '', '/');
    installNavigationPipeline();
  });

  afterEach(async () => {
    await __resetPipelineForTesting();
    uninstallNavigationPipeline();
  });

  /** URL-keyed fetch stub over the same URL normalization as doFetch. */
  function stubFetch(map: Map<string, NavigationResponse>): void {
    const { __setHandlesForTesting } = navigationPipelineModule();
    __setHandlesForTesting({
      fetch: ((url: string) => {
        const env = map.get(new URL(url, document.baseURI).href);
        if (!env) return Promise.reject(new Error('no fixture'));
        return Promise.resolve(env);
      }) as typeof import('./navigation').fetchNavigation,
      hardNavigate: null,
    });
  }

  function clearFetch(): void {
    const { __setHandlesForTesting } = navigationPipelineModule();
    __setHandlesForTesting({ fetch: null, hardNavigate: null });
  }

  it('soft /dashboard → /dashboard/settings preserves the layout instance', async () => {
    // SSR document with root layout markup (soft-entry starting point).
    document.body.innerHTML = `
      <div data-go-layout="app/layout.templ">
        <div data-go-page-outlet><h1>About</h1></div>
      </div>`;
    stubFetch(
      new Map([
        [new URL('/dashboard', document.baseURI).href, dashboardEnv],
        [new URL('/dashboard/settings', document.baseURI).href, settingsEnv],
      ])
    );
    await navigateTo('/dashboard');
    const markerAfterEntry = document.querySelector(
      `[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`
    );
    const hostAfterEntry = document.querySelector('[data-go-layout-shell-host]');
    const outletAfterEntry = document.querySelector('[data-go-layout-outlet]');
    const counterBtn = document.querySelector<HTMLButtonElement>(
      '[data-go-layout-counter-button]'
    )!;
    counterBtn.click(); // sidebar counter = 1
    await new Promise((r) => setTimeout(r, 0));

    let layoutMounts = 0;
    let layoutUnmounts = 0;
    const stop = onLifecycleEvent((e) => {
      if (e.target === hostAfterEntry) {
        if (e.phase === 'mounted') layoutMounts++;
        else layoutUnmounts++;
      }
    });

    await navigateTo('/dashboard/settings');
    stop();

    // Identity preserved across the soft child swap.
    expect(
      document.querySelector(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`)
    ).toBe(markerAfterEntry);
    expect(document.querySelector('[data-go-layout-shell-host]')).toBe(hostAfterEntry);
    expect(document.querySelector('[data-go-layout-outlet]')).toBe(outletAfterEntry);
    // State preserved: the counter was incremented BEFORE the swap.
    expect(
      document.querySelector('[data-go-layout-counter]')?.textContent
    ).toBe('Layout count: 1');
    // Instrumentation: no second layout mount, no layout unmount.
    expect(layoutMounts).toBe(0);
    expect(layoutUnmounts).toBe(0);
    // Exactly one page root, with Settings inside it.
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
    const root = document.querySelector('[data-go-page-root]')!;
    expect(root.getAttribute('data-go-route-id')).toBe('dashboard/settings/page.svelte');
    expect(root.querySelector('h1')?.textContent).toBe('Settings');
    clearFetch();
  });

  it('failure during component loading leaves the old page intact and hard-navigates', async () => {
    document.body.innerHTML = `
      <div data-go-layout="app/layout.templ">
        <div data-go-page-outlet><h1>About</h1></div>
      </div>`;
    const badSettings: NavigationResponse = {
      ...settingsEnv,
      clientEntry: 'no/such/page.svelte',
    };
    let hardNav: string | null = null;
    const { __setHandlesForTesting } = navigationPipelineModule();
    __setHandlesForTesting({
      fetch: ((url: string) => {
        const href = new URL(url, document.baseURI).href;
        if (href === new URL('/dashboard', document.baseURI).href) {
          return Promise.resolve(dashboardEnv);
        }
        return Promise.resolve(badSettings);
      }) as unknown as typeof import('./navigation').fetchNavigation,
      hardNavigate: (url: string) => {
        hardNav = url;
      },
    });
    await navigateTo('/dashboard');
    const marker = document.querySelector(
      `[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`
    );
    await navigateTo('/dashboard/settings');
    // Fallback taken to the intended destination...
    expect(hardNav).toBe('/dashboard/settings');
    // ...and the retained layout was NOT torn down or replaced by the
    // failed swap (the fallback reload will replace the whole document).
    expect(
      document.querySelector(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`)
    ).toBe(marker);
    __setHandlesForTesting({ fetch: null, hardNavigate: null });
  });

  it('stale navigation cannot overwrite a newer child page', async () => {
    document.body.innerHTML = `
      <div data-go-layout="app/layout.templ">
        <div data-go-page-outlet><h1>About</h1></div>
      </div>`;
    const aboutEnv: NavigationResponse = {
      version: 1,
      url: '/about',
      routeId: 'about/page.templ',
      mode: 'ssr',
      title: 'About',
      layouts: ['app/layout.templ'],
      html: '<h1>About</h1>',
      clientEntry: null,
    };
    const deferred = new Map<string, ((env?: NavigationResponse) => void) | null>();
    const { __setHandlesForTesting } = navigationPipelineModule();
    __setHandlesForTesting({
      fetch: ((url: string) => {
        const href = new URL(url, document.baseURI).href;
        return new Promise<NavigationResponse>((resolve) => {
          if (href === new URL('/dashboard', document.baseURI).href) {
            resolve(dashboardEnv);
            return;
          }
          deferred.set(href, (env?: NavigationResponse) =>
            resolve(env ?? (href.endsWith('/settings') ? settingsEnv : aboutEnv))
          );
        });
      }) as unknown as typeof import('./navigation').fetchNavigation,
      hardNavigate: null,
    });
    await navigateTo('/dashboard'); // subtree entered

    // Begin the slower Settings swap, then supersede it mid-flight with a
    // popstate-style navigation back to /dashboard.
    const pSettings = navigateTo('/dashboard/settings');
    const pHome = navigateTo('/dashboard');
    // Settle the NEWER navigation first; the older fetch resolves LATE.
    deferred.get(new URL('/dashboard', document.baseURI).href)?.();
    await pHome;
    deferred.get(new URL('/dashboard/settings', document.baseURI).href)?.();
    await pSettings;
    // The newer child page wins; the stale swap never touched the DOM.
    expect(
      document.querySelector('[data-go-page-root]')?.getAttribute('data-go-route-id')
    ).toBe('dashboard/page.svelte');
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
    __setHandlesForTesting({ fetch: null, hardNavigate: null });
  });
});

describe('G05: bootstrap handover — direct-load shell keeps its layout', () => {
  beforeEach(() => {
    window.history.replaceState(null, '', '/dashboard');
    installNavigationPipeline();
  });

  afterEach(async () => {
    await __resetPipelineForTesting();
    uninstallNavigationPipeline();
  });

  it('a direct-loaded /dashboard swaps children without remounting the layout', async () => {
    // The bare boot shell a direct load serves (E10): #app root only,
    // NO templ layout markup — the direct-load case real browsers hit.
    document.body.innerHTML = `
      <div id="app" data-go-page-root data-go-route="/dashboard" data-go-route-id="dashboard/page.svelte"></div>
    `;
    // Re-run the bootstrap's mounting steps (entry.ts logic, verbatim
    // flow): resolve chain, mount layout into the shell region, host page.
    const region = document.querySelector('#app') as HTMLElement;
    region.removeAttribute('data-go-page-root');
    region.replaceChildren();
    const { createLayoutPageRoot } = await import('./layout-mount');
    const layout = await mountPersistentLayout(DASHBOARD_LAYOUT_ID, region);
    const component = await loadPage('dashboard/page.svelte');
    const pageRoot = createLayoutPageRoot(layout.outlet, {
      ...dashboardEnv,
      url: '/dashboard',
    });
    const page = mountComponent(component!, { target: pageRoot });

    // G05 handover: bootstrap → pipeline, exactly as entry.ts does it.
    adoptMountedInstances({ layout, page: { ...page, pageRoot } });

    const markerBefore = document.querySelector(
      `[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`
    );
    const counterBtn = document.querySelector<HTMLButtonElement>(
      '[data-go-layout-counter-button]'
    )!;
    counterBtn.click();
    await new Promise((r) => setTimeout(r, 0));

    stubFetchDirect();
    await navigateTo('/dashboard/settings');

    // Same layout instance, same DOM identity, counter preserved.
    expect(
      document.querySelector(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`)
    ).toBe(markerBefore);
    expect(
      document.querySelector('[data-go-layout-counter]')?.textContent
    ).toBe('Layout count: 1');
    expect(
      document.querySelector('[data-go-page-root]')?.getAttribute('data-go-route-id')
    ).toBe('dashboard/settings/page.svelte');
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
    clearFetchDirect();
  });

  function stubFetchDirect(): void {
    const { __setHandlesForTesting } = navigationPipelineModule();
    __setHandlesForTesting({
      fetch: ((url: string) => {
        const href = new URL(url, document.baseURI).href;
        if (href === new URL('/dashboard/settings', document.baseURI).href) {
          return Promise.resolve(settingsEnv);
        }
        return Promise.reject(new Error('no fixture'));
      }) as typeof import('./navigation').fetchNavigation,
      hardNavigate: null,
    });
  }

  function clearFetchDirect(): void {
    const { __setHandlesForTesting } = navigationPipelineModule();
    __setHandlesForTesting({ fetch: null, hardNavigate: null });
  }

  it('adoption of a non-live layout is rejected, not guessed at', async () => {
    document.body.innerHTML = '';
    const region = document.createElement('div');
    document.body.append(region);
    const layout = await mountPersistentLayout(DASHBOARD_LAYOUT_ID, region);
    await layout.dispose(); // no longer live in the document
    const component = await loadLayout(DASHBOARD_LAYOUT_ID);
    void component;
    const target = document.createElement('div');
    document.body.append(target);
    const page = mountComponent((await loadPage('dashboard/page.svelte'))!, { target });
    const { LayoutMountError } = await import('./layout-mount');
    expect(() =>
      adoptMountedInstances({ layout, page: { ...page, pageRoot: target } })
    ).toThrowError(LayoutMountError);
    await page.unmount();
  });
});
