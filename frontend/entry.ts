// Client entry point for the go-meta framework.
//
// E03-E05: mount the CSR page for the current document from the route
// import map — no routing yet (SPA navigation arrives in later phases).
// Route selection reads the shell's data-go-route-id attribute here in
// the bootstrap; the general-purpose mount/unmount lifecycle lives in
// svelte-mounter.ts, and lazy component loading lives in
// route-import-map.ts.
import { mountComponent } from './svelte-mounter';
import type { MountHandle } from './svelte-mounter';
import { loadPage } from './route-import-map';

const MountID = 'app';
const RouteIDAttr = 'data-go-route-id';

// Handle for the live page instance; later phases' navigation runtime
// unmounts this before mounting the next route.
let currentPage: MountHandle | null = null;

async function boot(): Promise<void> {
  const target = document.getElementById(MountID);
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

void boot();
