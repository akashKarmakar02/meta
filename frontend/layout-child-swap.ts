// G05: child-only page swap inside a RETAINED persistent layout
// (blueprint G05: "Switch /dashboard→/dashboard/settings by replacing
// child only. Done: sidebar counter remains unchanged").
//
// This module owns the SWAP operation of one page inside a mounted
// layout's outlet — the G03 ownership boundary's runtime half:
//
//   ┌───────────────────────────────────────────────────────────────┐
//   │ layout component (Svelte-owned) — UNTOUCHED by a child swap     │
//   │  sidebar, counter state, [data-go-layout-outlet] element        │
//   │  ┌───────────────────────────────────────────────────────────┐ │
//   │  │ outlet interior        NAVIGATION RUNTIME owned (G05)      │ │
//   │  │  [data-go-page-root] + page component  ← ONLY this changes │ │
//   │  └───────────────────────────────────────────────────────────┘ │
//   └───────────────────────────────────────────────────────────────┘
//
// What belongs to G05 vs neighbors:
// - G04 mounts the layout and hosts the FIRST page in its outlet.
// - G05 (here) swaps the page for a sibling under the SAME layout:
//   unmount outgoing → await its teardown → mount incoming into the same
//   outlet. The layout instance, its host, its marker element, and its
//   outlet ELEMENT are preserved by identity.
// - G06 covers the return navigation /dashboard/settings → /dashboard
//   as an accepted scenario; mechanically it is this same swap.
// - G07 unmounts the layout on subtree exit; G08 is fresh state after
//   such an exit. Neither is implemented here.
//
// Retention is decided by the CALLER with G01's mounted-versus-declared
// model (client-layout sequences, see layout-stack.clientLayoutSequence):
// a layout listed in the envelope proves nothing; only a caller-supplied
// retained LayoutInstanceHandle — whose host/marker/outlet are live DOM
// identities — is swapped within. Never call this for a layout that is
// not already mounted.

import { mountComponent, type MountHandle } from './svelte-mounter';
import { createLayoutPageRoot } from './layout-mount';
import type { NavigationResponse } from './types';

/** Typed failure of a child-page swap attempt. */
export class ChildSwapError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ChildSwapError';
  }
}

/** Handle to the single active child page inside a retained layout. */
export interface ChildPageHandle extends MountHandle {
  /** The runtime-owned page mount root this page lives in. */
  readonly pageRoot: HTMLElement;
}

/**
 * Replace the child page inside `layout.outlet` with the destination page.
 *
 * Contract (blueprint G05 acceptance, /dashboard → /dashboard/settings):
 * - `layout` must be a RETAINED, still-mounted persistent layout instance
 *   (caller-verified via the mounted-vs-declared model). Its component
 *   instance, host element, marker, and outlet ELEMENT are preserved by
 *   identity — nothing Svelte-owned is cleared or replaced.
 * - `outgoing` (the currently hosted child page handle) is UNMOUNTED and
 *   AWAITED first: the incoming page mounts into a torn-down outlet
 *   interior, never beside a dying outro.
 * - `component` must already be RESOLVED (dynamic import done before any
 *   teardown — a failed load must leave the old page intact; the caller
 *   resolved it via the generated registry).
 * - Exactly ONE page root exists after success, inside the retained
 *   layout's outlet, stamped with the destination route's data-go-route /
 *   data-go-route-id. Repeated swaps replace, never stack.
 * - Duplicate-mount protection: svelte-mounter rejects mounting into an
 *   occupied target; the outgoing page's awaited unmount frees the target
 *   first, so the invariant is structural, not advisory.
 *
 * Failure safety: if the incoming mount throws AFTER the outgoing page
 * was already unmounted, the outlet interior is left empty and the error
 * propagates — the caller falls back to hard navigation (never a
 * half-swapped page presented as success). Before the first DOM change
 * nothing has happened yet: unknown/null components fail with no effect.
 */
export async function swapLayoutChildPage(
  layout: { readonly outlet: HTMLElement },
  env: NavigationResponse,
  component: unknown,
  outgoing?: MountHandle | null,
  assertCurrent?: () => void
): Promise<ChildPageHandle> {
  if (!component) {
    // Resolve-before-teardown discipline mirrored from csr-mount: fail
    // before any teardown so the current page stays intact for fallback.
    throw new ChildSwapError(
      `no page component resolved for clientEntry ${JSON.stringify(env.clientEntry)}`
    );
  }

  const outlet = layout.outlet;

  // Unmount the outgoing page and AWAIT its teardown (E04 outro-aware
  // unmount) before touching the interior. Idempotent handle: a caller
  // may pass an already-unmounted handle (double-swap protection).
  if (outgoing) {
    await outgoing.unmount();
  }
  assertCurrent?.();

  // Fresh page mount root in the outlet INTERIOR. createLayoutPageRoot
  // clears the runtime-owned interior (any remnant of the outgoing page
  // root) and stamps the destination route metadata.
  const pageRoot = createLayoutPageRoot(outlet, env);

  const handle = mountComponent(component as Parameters<typeof mountComponent>[0], {
    target: pageRoot,
  });
  return { ...handle, pageRoot };
}
