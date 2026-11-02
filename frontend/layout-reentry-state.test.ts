// G08 tests: fresh layout state on re-entry after a completed subtree
// exit (blueprint G08: "Switch /about→/dashboard, fresh layout state
// expected. Done: new mount count, clean state").
//
// Scope: the state lifecycle across a full cycle — persist within the
// subtree, reset after a completed exit, fresh instance identity on the
// re-entry mount, exact-once unmount/mount instrumentation, no leaks
// across repeated cycles — on both entry paths the DOM contract permits
// (direct-load CSR shell adoption and soft navigation from an SSR page).
//
// The state machinery itself is G07's (the exit disposes the instance;
// mountPersistentLayout always creates a fresh host + fresh $state).
// This gate is verification-only: no production changes were needed;
// these tests pin the behavior so later phases cannot regress it.

import { describe, it, expect, beforeEach, afterEach, beforeAll } from 'vitest';
import {
  mountPersistentLayout,
  createLayoutPageRoot,
  type LayoutInstanceHandle,
} from './layout-mount';
import { mountComponent, onLifecycleEvent, liveInstanceCount } from './svelte-mounter';
import { loadPage } from './route-import-map';
import { swapLayoutChildPage } from './layout-child-swap';
import { unmountLayoutSubtree } from './layout-subtree-exit';
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

const csrSettings: NavigationResponse = {
  ...csrDashboard,
  url: '/dashboard/settings',
  routeId: 'dashboard/settings/page.svelte',
  title: 'Settings',
  clientEntry: 'dashboard/settings/page.svelte',
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
  const outlet = document.querySelector("[data-go-page-outlet]");
  return outlet as HTMLElement;
}

async function settle(): Promise<void> {
  await new Promise((r) => setTimeout(r, 0));
}

describe('G08 unit contract: unmountLayoutSubtree exits cleanly', () => {
  let region: HTMLElement;

  beforeEach(() => {
    document.body.innerHTML = '';
    region = document.createElement('div');
    document.body.append(region);
  });

  it('a completed exit destroys the old instance: fresh re-entry resets counter state', async () => {
    // Mount subtree; bump counter to a nonzero value.
    const first = await mountPersistentLayout(DASHBOARD_LAYOUT_ID, region);
    const dashboardComp = await loadPage(csrDashboard.clientEntry!);
    const firstRoot = createLayoutPageRoot(first.outlet, csrDashboard);
    const firstPage = mountComponent(dashboardComp!, { target: firstRoot });
    const button = () =>
      document.querySelector<HTMLButtonElement>('[data-go-layout-counter-button]')!;
    const counterText = () =>
      document.querySelector('[data-go-layout-counter]')?.textContent ?? '';
    expect(counterText()).toContain('0'); // initial value
    button().click();
    await settle();
    button().click();
    await settle();
    expect(counterText()).toContain('2'); // persisted, nonzero

    // Swap to Settings inside the subtree: value persists.
    const settingsComp = await loadPage(csrSettings.clientEntry!);
    const settingsPage = await swapLayoutChildPage(first, csrSettings, settingsComp!, firstPage);
    expect(counterText()).toContain('2'); // G05/G06 persistence intact

    // Complete exit: old instance destroyed.
    await unmountLayoutSubtree(first, settingsPage);

    // Re-enter: fresh host, fresh instance, initial counter.
    const second = await mountPersistentLayout(DASHBOARD_LAYOUT_ID, region);
    expect(second.host).not.toBe(first.host); // new node identity
    const freshComp = await loadPage(csrDashboard.clientEntry!);
    const secondRoot = createLayoutPageRoot(second.outlet, csrDashboard);
    const secondPage = mountComponent(freshComp!, { target: secondRoot });
    expect(counterText()).toContain('0'); // reset, not previous value
    // New instance remains reactive.
    button().click();
    await settle();
    expect(counterText()).toContain('1');
    await unmountLayoutSubtree(second, secondPage);
  });

  it('exact-once unmount of the old and mount of the new layout across a cycle', async () => {
    const events: { phase: string; target: Element }[] = [];
    const stop = onLifecycleEvent((e) => events.push({ ...e }));

    const first = await mountPersistentLayout(DASHBOARD_LAYOUT_ID, region);
    const comp = await loadPage(csrDashboard.clientEntry!);
    const root = createLayoutPageRoot(first.outlet, csrDashboard);
    const page = mountComponent(comp!, { target: root });
    const firstHost = first.host;

    await unmountLayoutSubtree(first, page);
    const second = await mountPersistentLayout(DASHBOARD_LAYOUT_ID, region);
    const comp2 = await loadPage(csrDashboard.clientEntry!);
    const root2 = createLayoutPageRoot(second.outlet, csrDashboard);
    const page2 = mountComponent(comp2!, { target: root2 });
    stop();

    const hostEvents = events.filter((e) => e.target === firstHost);
    expect(hostEvents.filter((e) => e.phase === 'mounted').length).toBe(1);
    expect(hostEvents.filter((e) => e.phase === 'unmounted').length).toBe(1); // exact-once destroy
    const secondHostEvents = events.filter((e) => e.target === second.host);
    expect(secondHostEvents.filter((e) => e.phase === 'mounted').length).toBe(1); // exact-once fresh mount
    await unmountLayoutSubtree(second, page2);
  });

  it('repeated entry/exit/re-entry cycles leak nothing', async () => {
    const baseline = liveInstanceCount();
    for (let i = 0; i < 3; i++) {
      const l = await mountPersistentLayout(DASHBOARD_LAYOUT_ID, region);
      const c = await loadPage(csrDashboard.clientEntry!);
      const r = createLayoutPageRoot(l.outlet, csrDashboard);
      const p = mountComponent(c!, { target: r });
      document
        .querySelector<HTMLButtonElement>('[data-go-layout-counter-button]')!
        .click();
      await settle();
      document
        .querySelector<HTMLButtonElement>('[data-go-layout-counter-button]')!
        .click();
      await settle();
      await unmountLayoutSubtree(l, p);
    }
    expect(liveInstanceCount()).toBe(baseline);
    expect(document.querySelectorAll('[data-go-layout-shell-host]').length).toBe(0);
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(0);
  });
});

describe('G08 pipeline: fresh state on re-entry via soft navigation', () => {
  beforeEach(() => {
    window.history.replaceState(null, '', '/');
    installSSRDocument();
    pipelineRef!.startSoftNavigation();
  });

  afterEach(async () => {
    await pipelineRef!.__resetPipelineForTesting();
    pipelineRef!.stopSoftNavigation();
    document.body.innerHTML = '';
  });

  it('counter persists within the subtree, resets after exit + re-entry', async () => {
    stubFetch(
      new Map([
        [new URL('/dashboard', document.baseURI).href, csrDashboard],
        [new URL('/dashboard/settings', document.baseURI).href, csrSettings],
        [new URL('/about', document.baseURI).href, ssrAbout],
      ])
    );

    await pipelineRef!.navigateTo('/dashboard');
    const counter = () =>
      document.querySelector('[data-go-layout-counter]')?.textContent ?? '';
    const button = () =>
      document.querySelector<HTMLButtonElement>('[data-go-layout-counter-button]')!;
    expect(counter()).toContain('0');
    button().click();
    await settle();
    button().click();
    await settle();
    expect(counter()).toContain('2');

    const firstHost = document.querySelector('[data-go-layout-shell-host]');

    // Within-subtree swap: value persists (G05/G06).
    await pipelineRef!.navigateTo('/dashboard/settings');
    expect(counter()).toContain('2');

    // Exit + re-entry through the pipeline's real paths.
    await pipelineRef!.navigateTo('/about');
    await expect
      .poll(() => document.querySelectorAll('[data-go-layout-shell-host]').length)
      .toBe(0);

    await pipelineRef!.navigateTo('/dashboard');
    await expect
      .poll(() => document.querySelectorAll('[data-go-layout-shell-host]').length)
      .toBe(1);

    // Fresh instance: reset counter, different node identity, still reactive.
    expect(counter()).toContain('0');
    const secondHost = document.querySelector('[data-go-layout-shell-host]');
    expect(secondHost).not.toBe(firstHost);
    expect(document.querySelectorAll('[data-go-page-root]').length).toBe(1);
    button().click();
    await settle();
    expect(counter()).toContain('1');
    clearFetch();
  });

  it('Back/Forward does not resurrect old component state after a completed exit', async () => {
    stubFetch(
      new Map([
        [new URL('/dashboard', document.baseURI).href, csrDashboard],
        [new URL('/about', document.baseURI).href, ssrAbout],
      ])
    );

    await pipelineRef!.navigateTo('/dashboard');
    document
      .querySelector<HTMLButtonElement>('[data-go-layout-counter-button]')!
      .click();
    await settle();
    expect(
      document.querySelector('[data-go-layout-counter]')?.textContent ?? ''
    ).toContain('1');

    await pipelineRef!.navigateTo('/about'); // exit under test
    await expect
      .poll(() => document.querySelectorAll('[data-go-layout-shell-host]').length)
      .toBe(0);
    await pipelineRef!.navigateTo('/dashboard');
    await expect
      .poll(() => document.querySelectorAll('[data-go-layout-shell-host]').length)
      .toBe(1);
    expect(
      document.querySelector('[data-go-layout-counter]')?.textContent ?? ''
    ).toContain('0');
    clearFetch();
  });
});
