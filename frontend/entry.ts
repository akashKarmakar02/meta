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
import { mountComponent } from './svelte-mounter';
import type { MountHandle } from './svelte-mounter';
import { loadPage } from './route-import-map';
import { startSoftNavigation } from './navigation-pipeline';

const PageRootSelector = '[data-go-page-root]';
const RouteIDAttr = 'data-go-route-id';

// Handle for the live page instance; later phases' navigation runtime
// unmounts this before mounting the next route.
let currentPage: MountHandle | null = null;

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
  currentPage = mountComponent(component, { target });
}

void boot().finally(() => {
  // Activate SPA navigation once the initial mount is underway. The pipeline
  // takes over page ownership for all subsequent navigations.
  startSoftNavigation();
});
