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
//
// G04 adds the same façade over the generated layout maps: client
// (layout.svelte) components addressed by stable layout ID, plus each
// route's declared ancestry for bootstrap-time mounting where no
// navigation envelope exists.
import type { Component } from 'svelte';
import { pageLoaders, layoutLoaders, routeLayouts } from './route-registry.generated';

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

// ---- G04: client layout resolution over the generated layout maps ----

/** A persistent client layout component (layout.svelte). */
export type LayoutComponent = Component<any, any, any>;

/** Module namespace produced by a dynamic layout import. */
export interface LayoutModule {
  default: LayoutComponent;
}

/**
 * The declared layout ancestry of a route ID, outermost -> innermost —
 * the same chain a navigation envelope's `layouts` field carries. Empty
 * for routes with no layouts or unknown route IDs.
 */
export function declaredLayoutsFor(routeId: string): string[] {
  const chain = routeLayouts[routeId];
  return Array.isArray(chain) ? [...chain] : [];
}

/**
 * Whether the layout map knows a layout ID AND it is a client
 * (layout.svelte) component. SSR (.templ) layouts appear in ancestry
 * data but never load client-side: they are mounted by the server.
 */
export function hasLayout(layoutId: string): boolean {
  return Object.hasOwn(layoutLoaders, layoutId);
}

/**
 * Resolve a layout ID to its client component, loading it lazily.
 * Explicit missing result (null) for unknown or non-client layout IDs —
 * callers decide how to react; nothing is ever guessed.
 */
export async function loadLayout(layoutId: string): Promise<LayoutComponent | null> {
  if (!Object.hasOwn(layoutLoaders, layoutId)) {
    return null;
  }
  const mod = await layoutLoaders[layoutId]();
  return (mod as LayoutModule).default;
}
