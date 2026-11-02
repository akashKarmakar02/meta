// G06 tests: remount-free RETURN navigation /dashboard/settings →
// /dashboard inside the retained persistent layout (blueprint G06:
// "Switch back without remount of shared layout. Done: layout mountCount
// still 1").
//
// G05 proved the forward child swap; G06 accepts the return direction as
// its own scenario. Mechanically it is the same retained-child-swap
// branch, so this suite is deliberately regression-focused: it pins the
// outcomes the blueprint's acceptance criterion names — same layout
// instance, mountCount still 1, sidebar state preserved, exactly one
// child page, correct destination metadata — on the two production paths
// that can host the return leg (F12 soft navigation and the E03 bootstrap
// handover from a DIRECT-LOADED settings shell).
//
// G07 (graceful layout unmount on subtree exit) and G08 (fresh state on
// re-entry) are out of scope: no test here navigates OUT of the dashboard
// subtree.

import { describe, it, expect, beforeEach, afterEach, beforeAll } from 'vitest';
import { mountPersistentLayout, type LayoutInstanceHandle } from './layout-mount';
import { mountComponent, onLifecycleEvent, liveInstanceCount } from './svelte-mounter';
import { loadPage, loadLayout } from './route-import-map';
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
  layouts: [DASHBOARD_LAYOUT_ID],
  html: '',
  clientEntry: 'dashboard/page.svelte',
};

const settingsEnv: NavigationResponse = {
  version: 1,
  url: '/dashboard/settings',
  routeId: 'dashboard/settings/page.svelte',
  mode: 'csr',
  title: 'Settings',
  layouts: [DASHBOARD_LAYOUT_ID],
  html: '',
  clientEntry: 'dashboard/settings/page.svelte',
};

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

/** The pipeline module, imported lazily like production code (single load). */
function navigationPipelineModule(): typeof import('./navigation-pipeline') {
  return pipelineRef!;
}
let pipelineRef: typeof import('./navigation-pipeline') | null = null;
beforeAll(async () => {
  pipelineRef = await import('./navigation-pipeline');
});

describe('G06: remount-free return navigation inside the subtree', () => {
  beforeEach(() => {
    window.history.replaceState(null, '', '/');
    installNavigationPipeline();
  });

  afterEach(async () => {
    await __resetPipelineForTesting();
    uninstallNavigationPipeline();
  });

  it('soft /dashboard/settings → /dashboard keeps the layout instance, its state, and mountCount at 1', async () => {
    // Enter the subtree AT SETTINGS first (SSR document, G04 subtree-entry
    // branch hosts the Settings page in the freshly mounted layout).
    document.body.innerHTML = `
      <div data-go-layout="app/layout.templ">
        <div data-go-page-outlet><h1>About</h1></div>
      </div>`;
    stubFetch(
      new Map([
        [new URL('/dashboard/settings', document.baseURI).href, settingsEnv],
        [new URL('/dashboard', document.baseURI).href, dashboardEnv],
      ])
    );
    await navigateTo('/dashboard/settings');
    const markerBefore = document.querySelector(
      `[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`
    );
    const hostBefore = document.querySelector('[data-go-layout-shell-host]');
    const outletBefore = document.querySelector('[data-go-layout-outlet]');
    const sidebarBefore = document.querySelector('[data-go-dashboard-sidebar]');
    const btn = document.querySelector<HTMLButtonElement>(
      '[data-go-layout-counter-button]'
    )!;
    btn.click(); // sidebar counter = 1
    btn.click(); // sidebar counter = 2
    await new Promise((r) => setTimeout(r, 0));

    // Instrumentation: count layout lifecycle events during the return leg.
    let layoutMounts = 0;
    let layoutUnmounts = 0;
    const stop = onLifecycleEvent((e) => {
      if (e.target === hostBefore) {
        if (e.phase === 'mounted') layoutMounts++;
        else layoutUnmounts++;
      }
    });

    await navigateTo('/dashboard'); // the G06 return leg under test
    stop();

    // Same component instance by every DOM identity it owns.
    expect(
      document.querySelector(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`)
    ).toBe(markerBefore);
    expect(document.querySelector('[data-go-layout-shell-host]')).toBe(hostBefore);
    expect(document.querySelector('[data-go-layout-outlet]')).toBe(outletBefore);
    expect(document.querySelector('[data-go-dashboard-sidebar]')).toBe(sidebarBefore);
    // State preserved and still reactive: the swap never touched it.
    expect(
      document.querySelector('[data-go-layout-counter]')?.textContent
    ).toBe('Layout count: 2');
    // Blueprint acceptance criterion, instrumented: mountCount still 1 —
    // zero additional layout mounts, zero unmounts during the return.
    expect(layoutMounts).toBe(0);
    expect(layoutUnmounts).toBe(0);
    expect(document.querySelectorAll('[data-go-layout-shell-host]').length).toBe(1);

    // Settings left, Dashboard arrived, exactly one page root in the SAME outlet.
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
    const root = document.querySelector('[data-go-page-root]')!;
    expect(root.getAttribute('data-go-route')).toBe('/dashboard');
    expect(root.getAttribute('data-go-route-id')).toBe('dashboard/page.svelte');
    expect(root.querySelector('h1')?.textContent).toBe('Dashboard');
    expect(root.closest('[data-go-layout-outlet]')).toBe(outletBefore);
    // The Settings page component is gone entirely.
    expect(document.querySelector('[data-go-page="dashboard/settings"]')).toBeNull();
    clearFetch();
  });

  it('repeated return cycles Settings → Dashboard → Settings → Dashboard stay identity-stable', async () => {
    document.body.innerHTML = `
      <div data-go-layout="app/layout.templ">
        <div data-go-page-outlet><h1>About</h1></div>
      </div>`;
    stubFetch(
      new Map([
        [new URL('/dashboard/settings', document.baseURI).href, settingsEnv],
        [new URL('/dashboard', document.baseURI).href, dashboardEnv],
      ])
    );
    await navigateTo('/dashboard/settings');
    const markerBefore = document.querySelector(
      `[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`
    );

    const baseline = liveInstanceCount();
    await navigateTo('/dashboard');
    await navigateTo('/dashboard/settings');
    await navigateTo('/dashboard');

    // No remount across the cycle: same marker instance, one layout, one
    // page root showing the LAST destination.
    expect(
      document.querySelector(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`)
    ).toBe(markerBefore);
    expect(document.querySelectorAll('[data-go-layout-shell-host]').length).toBe(1);
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
    expect(
      document.querySelector('[data-go-page-root]')?.getAttribute('data-go-route-id')
    ).toBe('dashboard/page.svelte');
    expect(
      document.querySelector('[data-go-page-root] h1')?.textContent
    ).toBe('Dashboard');
    // No leaked instances: each swap's outgoing page unmounted before the
    // incoming mount, so the live count returns to its quiescent baseline.
    expect(liveInstanceCount()).toBe(baseline);
    clearFetch();
  });

  it('popstate-style return (push: false) reuses the retained layout too', async () => {
    // Browser Back/Forward re-enters applyEnvelope through the same
    // retention planning with push: false; the return leg must retain just
    // the same. Enter at Settings, "forward" to Dashboard, then restore.
    document.body.innerHTML = `
      <div data-go-layout="app/layout.templ">
        <div data-go-page-outlet><h1>About</h1></div>
      </div>`;
    stubFetch(
      new Map([
        [new URL('/dashboard/settings', document.baseURI).href, settingsEnv],
        [new URL('/dashboard', document.baseURI).href, dashboardEnv],
      ])
    );
    await navigateTo('/dashboard/settings', { push: true });
    await navigateTo('/dashboard', { push: true });
    const markerBefore = document.querySelector(
      `[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`
    );

    // History restoration back to Settings: no push, same retention rules.
    await navigateTo('/dashboard/settings', { push: false });
    expect(
      document.querySelector(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`)
    ).toBe(markerBefore);
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
    expect(
      document.querySelector('[data-go-page-root]')?.getAttribute('data-go-route-id')
    ).toBe('dashboard/settings/page.svelte');
    expect(
      document.querySelector('[data-go-page-root] h1')?.textContent
    ).toBe('Settings');
    clearFetch();
  });
});

describe('G06: return navigation after a direct load of /dashboard/settings', () => {
  beforeEach(() => {
    window.history.replaceState(null, '', '/dashboard/settings');
    installNavigationPipeline();
  });

  afterEach(async () => {
    await __resetPipelineForTesting();
    uninstallNavigationPipeline();
  });

  it('the adopted layout survives the return to /dashboard untouched', async () => {
    // The bare boot shell a DIRECT load of /dashboard/settings serves
    // (E10): #app root only, NO templ layout markup.
    document.body.innerHTML = `
      <div id="app" data-go-page-root data-go-route="/dashboard/settings" data-go-route-id="dashboard/settings/page.svelte"></div>
    `;
    // Re-run entry.ts's bootstrap steps verbatim: shell region hosts the
    // layout, Settings page mounts in its outlet, then handover.
    const region = document.querySelector('#app') as HTMLElement;
    region.removeAttribute('data-go-page-root');
    region.replaceChildren();
    const { createLayoutPageRoot } = await import('./layout-mount');
    const layout = await mountPersistentLayout(DASHBOARD_LAYOUT_ID, region);
    const component = await loadPage('dashboard/settings/page.svelte');
    const pageRoot = createLayoutPageRoot(layout.outlet, {
      ...settingsEnv,
      url: '/dashboard/settings',
    });
    const page = mountComponent(component!, { target: pageRoot });
    adoptMountedInstances({ layout, page: { ...page, pageRoot } }); // no duplicate handles

    const markerBefore = document.querySelector(
      `[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`
    );
    const btn = document.querySelector<HTMLButtonElement>(
      '[data-go-layout-counter-button]'
    )!;
    btn.click(); // sidebar counter = 1
    await new Promise((r) => setTimeout(r, 0));

    let layoutMounts = 0;
    const stop = onLifecycleEvent((e) => {
      if (e.target === layout.host && e.phase === 'mounted') layoutMounts++;
    });

    stubFetch(new Map([[new URL('/dashboard', document.baseURI).href, dashboardEnv]]));
    await navigateTo('/dashboard'); // return leg from the direct-loaded settings shell

    stop();
    expect(layoutMounts).toBe(0); // mountCount still 1 — the adoption held
    expect(
      document.querySelector(`[data-go-layout="${CSS.escape(DASHBOARD_LAYOUT_ID)}"]`)
    ).toBe(markerBefore);
    expect(
      document.querySelector('[data-go-layout-counter]')?.textContent
    ).toBe('Layout count: 1');
    expect(
      document.querySelector('[data-go-page-root]')?.getAttribute('data-go-route-id')
    ).toBe('dashboard/page.svelte');
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
    clearFetch();
  });
});
