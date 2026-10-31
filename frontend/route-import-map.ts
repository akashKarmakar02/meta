// Manually-coded client route import map (E05).
//
// Maps stable, source-derived route IDs (e.g. 'dashboard/page.svelte',
// matching the Go manifest's Route.ID) to lazy component loaders. Keys are
// logical identifiers — never evaluated paths — so a server response can
// only select an entry that exists here (blueprint §5: no arbitrary path
// import).
//
// E06 replaces this hand-written map with route-registry.generated.ts built
// from the same discovery manifest; the exported interface stays identical
// so consumers migrate without changes. Route selection (which ID belongs
// on the page) stays in the bootstrap/navigation runtime, not here: this
// module is pure data plus lookup, with no DOM or server coupling.
import type { Component } from 'svelte';

/** A lazily-loaded client-rendered page component. */
export type PageComponent = Component<any, any, any>;

/** Module namespace produced by a dynamic page import. */
export interface PageModule {
  default: PageComponent;
}

/** Lazy loader for one client-rendered page. */
export type PageLoader = () => Promise<PageModule>;

// Manual import map for Phase 1's two CSR routes. Import() keeps each page
// out of the entry bundle until its route actually renders.
const pages: Readonly<Record<string, PageLoader>> = {
  'dashboard/page.svelte': () => import('../app/dashboard/page.svelte'),
  'dashboard/settings/page.svelte': () => import('../app/dashboard/settings/page.svelte'),
};

/**
 * Whether the import map knows a route ID. Loaders exist only for IDs the
 * discovery manifest produced; unknown IDs are never guessed.
 */
export function hasPage(routeId: string): boolean {
  return Object.hasOwn(pages, routeId);
}

/**
 * Resolve a route ID to its page component, loading it lazily.
 *
 * Returns an explicit missing result (null) for unknown route IDs — no
 * silent fallback to another page. Callers decide how to react (the
 * navigation runtime will hard-navigate; the bootstrap simply mounts
 * nothing).
 */
export async function loadPage(routeId: string): Promise<PageComponent | null> {
  const loader = Object.hasOwn(pages, routeId) ? pages[routeId] : undefined;
  if (!loader) {
    return null;
  }
  const mod = await loader();
  return mod.default;
}
