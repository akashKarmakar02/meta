// Client entry point for the go-meta framework.
//
// E03/E04: mount the CSR component from a static import, no routing.
// Route selection lives here in the bootstrap — the general-purpose
// mount/unmount lifecycle is delegated to svelte-mounter.ts, and the
// route import map arrives with E05 (generated registry: E06).
import { mountComponent } from './svelte-mounter';
import type { MountHandle } from './svelte-mounter';
import Dashboard from '../app/dashboard/page.svelte';

const MountID = 'app';
const RouteIDAttr = 'data-go-route-id';

// Handle for the live page instance; later phases' navigation runtime
// unmounts this before mounting the next route.
let currentPage: MountHandle | null = null;

function mountDashboard(): void {
  const target = document.getElementById(MountID);
  if (!target) {
    // No CSR shell on this document (SSR page or plain HTML): nothing to do.
    return;
  }
  if (target.getAttribute(RouteIDAttr) !== 'dashboard/page.svelte') {
    // Not the dashboard route; components for other routes arrive with E05.
    return;
  }
  currentPage = mountComponent(Dashboard, { target });
}

mountDashboard();
