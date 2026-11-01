// Client entry point for the go-meta framework.
//
// E03-E10: mount the CSR page for the current document from the route
// import map. Route selection reads the shell's data-go-page-root mount
// point and its data-go-route-id attribute here in the bootstrap; the
// general-purpose mount/unmount lifecycle lives in svelte-mounter.ts,
// and lazy component loading lives in route-import-map.ts. Blueprint §4:
// the client queries [data-go-page-root] to find the app root.
//
// F12: after mounting the initial page, activate the soft-navigation
// pipeline (link interception → fetch → apply → history; §5.2). Its
// pipeline module owns navigation-time page handles from here on.
//
// G04: when the booted route's declared ancestry contains CLIENT
// (layout.svelte) layouts — a direct /dashboard or /dashboard/settings
// load, where the bare boot shell contains no layout markup at all —
// mount them once here, then host the page inside the innermost layout's
// [data-go-layout-outlet]. The shell's #app element stops being the page
// root and becomes the region hosting the mounted layout subtree; exactly
// one [data-go-page-root] exists afterward, inside the layout outlet.
import { mountComponent } from './svelte-mounter';
import type { MountHandle } from './svelte-mounter';
import { loadPage, declaredLayoutsFor, hasLayout, loadLayout } from './route-import-map';
import {
  mountPersistentLayout,
  createLayoutPageRoot,
  LayoutHostAttr,
  type LayoutInstanceHandle,
} from './layout-mount';
import { startSoftNavigation, adoptMountedInstances } from './navigation-pipeline';
import type { NavigationResponse } from './types';

const PageRootSelector = '[data-go-page-root]';
const RouteIDAttr = 'data-go-route-id';

// Handles for the live instances; later phases' navigation runtime
// unmounts these before mounting the next route.
let currentPage: MountHandle | null = null;
let currentLayout: LayoutInstanceHandle | null = null;

/** The boot page handle plus its mount root, for G05 handover below. */
let bootPage: { target: Element; mounted: boolean; unmount(o?: { outro?: boolean }): Promise<void>; pageRoot: HTMLElement } | null = null;

async function boot(): Promise<void> {
  const target = document.querySelector(PageRootSelector);
  if (!target) {
    // No CSR shell on this document (SSR page or plain HTML): nothing to do.
    return;
  }
  const routeId = target.getAttribute(RouteIDAttr);
  if (!routeId) {
    return;
  }
  const component = await loadPage(routeId);
  if (!component) {
    // Unknown route ID: mount nothing. No silent fallback to another
    // page; the navigation runtime (later phase) will hard-navigate.
    return;
  }

  // G04 direct-load layout entry: mount every CLIENT layout in the
  // declared chain (outermost first — one per chain position today),
  // then host the page inside the innermost one's outlet. SSR (.templ)
  // ancestry entries are server-mounted markup; on the bare boot shell
  // none exists, and that is fine: the shell document is the root region.
  const clientChain = declaredLayoutsFor(routeId).filter((id) => hasLayout(id));
  let pageTarget: HTMLElement = target as HTMLElement;
  let routeAttrs: { url: string; routeId: string };
  routeAttrs = {
    url: target.getAttribute('data-go-route') ?? window.location.pathname,
    routeId,
  };

  if (clientChain.length > 0) {
    // The shell root becomes the region hosting the layout subtree. It
    // stops being the page root (the page root moves inside the layout
    // outlet); nothing else about the shell contract changes.
    const region = target as HTMLElement;
    region.removeAttribute('data-go-page-root');
    region.replaceChildren();
    let outletRegion: ParentNode = region;
    for (const layoutId of clientChain) {
      currentLayout = await mountPersistentLayout(layoutId, outletRegion);
      outletRegion = currentLayout.outlet;
    }
    // Minimal envelope describing this document for createLayoutPageRoot's
    // metadata attributes.
    const env = {
      version: 1,
      url: routeAttrs.url,
      routeId,
      mode: 'csr' as const,
      title: '',
      layouts: [],
      html: '',
      clientEntry: routeId,
    } satisfies NavigationResponse;
    pageTarget = createLayoutPageRoot(currentLayout!.outlet, env);
    const handle = mountComponent(component, { target: pageTarget });
    bootPage = { ...handle, pageRoot: pageTarget };
    return;
  }

  currentPage = mountComponent(component, { target: pageTarget });
  if (clientChain.length === 0) {
    bootPage = { ...currentPage, pageRoot: pageTarget };
  }
}

void boot().finally(() => {
  // G05: when the bootstrap mounted a persistent layout subtree, hand its
  // instances — layout and first page — to the pipeline BEFORE activating
  // it. Ownership passes completely; from here the pipeline is the only
  // owner, so its first soft navigation can retain the layout (a child-only
  // swap /dashboard → /dashboard/settings keeps counter state even after a
  // direct load of the shell).
  if (currentLayout && bootPage) {
    try {
      adoptMountedInstances({ layout: currentLayout, page: bootPage });
    } catch {
      // Adoption only fails when the mounted instance is not verifiably
      // live (see adoptMountedInstances). The pipeline then starts with no
      // tracked layout: its first navigation mounts a fresh one — safe,
      // just not state-preserving. Never block activation on handover.
    }
    currentLayout = null;
    currentPage = null;
  }
  // Activate SPA navigation once the initial mount is underway. The pipeline
  // takes over page ownership for all subsequent navigations.
  startSoftNavigation();
});

// LayoutHostAttr re-exported for test discovery of the host element.
void LayoutHostAttr;
