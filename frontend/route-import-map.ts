// Client route resolution façade (E05→E06): exposes hasPage()/loadPage()
// over the route import map. Since E06 the map itself is generated from
// the Go route discovery manifest (frontend/route-registry.generated.ts,
// written by routegen at build time) — this file is hand-written logic
// only: lookup, explicit-missing semantics, and type narrowing.
//
// Keys are stable source-derived route IDs shared with the Go manifest;
// import() loaders are static per-route calls Vite analyzes at build
// time, so a server response can only select an entry that exists here
// (blueprint §5: no arbitrary path import).
import type { Component } from 'svelte';
import { pageLoaders } from './route-registry.generated';

/** A client-rendered page component. */
export type PageComponent = Component<any, any, any>;

/** Module namespace produced by a dynamic page import. */
export interface PageModule {
  default: PageComponent;
}

/** Lazy loader for one client-rendered page. */
export type PageLoader = () => Promise<PageModule>;

/**
 * Whether the route import map knows a route ID. Loaders exist only for
 * IDs the discovery manifest produced; unknown IDs are never guessed.
 */
export function hasPage(routeId: string): boolean {
  return Object.hasOwn(pageLoaders, routeId);
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
  if (!Object.hasOwn(pageLoaders, routeId)) {
    return null;
  }
  const mod = await pageLoaders[routeId]();
  return (mod as PageModule).default;
}
