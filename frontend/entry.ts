// Client entry point for the go-meta framework.
//
// E03: mount the CSR component from a static import, no routing. The
// dashboard page is currently the only statically-bound component; a route
// import map arrives with E05 and the generated registry with E06.
import { mount } from 'svelte';
import Dashboard from '../app/dashboard/page.svelte';

const MountID = 'app';
const RouteIDAttr = 'data-go-route-id';

function findMountTarget(): HTMLElement | null {
  return document.getElementById(MountID);
}

function mountDashboard(): void {
  const target = findMountTarget();
  if (!target) {
    // No CSR shell on this document (SSR page or plain HTML): nothing to do.
    return;
  }
  if (target.getAttribute(RouteIDAttr) !== 'dashboard/page.svelte') {
    // Not the dashboard route; components for other routes arrive with E05.
    return;
  }
  mount(Dashboard, { target });
}

mountDashboard();
