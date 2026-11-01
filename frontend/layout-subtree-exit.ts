// G07: graceful persistent-layout teardown when a navigation LEAVES a
// layout subtree (blueprint G07: "Switch /dashboard→/about, unmount
// nested layout. Done: destructor called once").
//
// This module owns the EXIT operation of a mounted persistent layout:
//
//   ┌───────────────────────────────────────────────────────────────┐
//   │ region (runtime-hosted)                                        │
//   │  <div data-go-layout-shell-host>  runtime-owned — REMOVED     │
//   │    layout component                 Svelte-owned — UNMOUNTED │
//   │      sidebar, counter, [data-go-layout-outlet]                 │
//   │        [data-go-page-root]       runtime-owned — UNMOUNTED   │
//   │          child page component                FIRST            │
//   └───────────────────────────────────────────────────────────────┘
//
// Teardown order is the ownership boundary inverted: the CHILD page is
// unmounted and awaited FIRST, then the layout component is unmounted
// (its one destructor call), then the runtime-owned host element is
// removed. Because the layout's marker, outlet ELEMENT, and page root
// all live inside the host, removing the host clears every stale layout
// marker, outlet, and page root from the document structurally — no
// sweeping removal of Svelte-owned or server-owned DOM anywhere else.
//
// What belongs to G07 vs neighbors:
// - G05/G06 swap the child INSIDE a retained layout; this module is
//   their opposite: nothing is retained, the whole subtree goes.
// - G08 (fresh state on RE-ENTRY) is not implemented here: a later
//   mountPersistentLayout call naturally produces a fresh instance.
// - The CALLER (navigation-pipeline) decides WHEN to exit, using G01's
//   mounted-vs-declared model: only a pipeline-TRACKED handle (never a
//   marker, never an env.layouts entry) reaches this module.
//
// Failure safety: a throw from the child unmount leaves the layout
// intact (caller falls back to hard navigation); a throw from the layout
// dispose leaves a possibly half-torn document that the caller's hard
// fallback replaces entirely. Either way the error propagates — never a
// half-exited subtree presented as success. The optional `guard` stops the
// exit at the child/layout boundary when superseded (F11): the document
// stays consistent for whichever navigation owns it now.

import type { MountHandle } from './svelte-mounter';
import type { LayoutInstanceHandle } from './layout-mount';

/** Typed failure of a layout-subtree exit attempt. */
export class LayoutExitError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'LayoutExitError';
  }
}

/**
 * Tear down a mounted persistent layout subtree: unmount the outgoing
 * child page (awaited), dispose the layout component (exactly one
 * destructor), and remove the runtime-owned host element.
 *
 * Contract (blueprint G07 acceptance, /dashboard → /about):
 * - `layout` must be a TRACKED, still-live persistent layout handle
 *   (caller-verified via the mounted-vs-declared model; the pipeline or
 *   the bootstrap adopted it — never a handle guessed from a marker).
 * - The CHILD page unmount completes BEFORE the layout unmount begins:
 *   a parent is never destroyed while its child still lives (the same
 *   innermost-first rule layoutStackTransition plans with).
 * - `guard`, when supplied, runs at the only await boundary between the
 *   two teardowns (F11 currency): if it returns true, the exit STOPS
 *   with the layout still mounted — its child page unmounted, outlet
 *   interior empty, a fully consistent document for whatever navigation
 *   superseded this one. The caller observes the early return and bails
 *   out through its own stale-handling.
 * - Both unmount handles are idempotent — a retried exit (double-dispose
 *   protection, or an already-unmounted child from a failed earlier
 *   attempt) never throws and never fires a second destructor.
 * - After success the document holds ZERO elements of this layout: no
 *   data-go-layout marker, no host, no outlet, no page root. A leftover
 *   marker throws LayoutExitError instead of passing silently — the
 *   caller hard-navigates rather than trusting a dirty document.
 */
export async function unmountLayoutSubtree(
  layout: LayoutInstanceHandle,
  outgoing?: MountHandle | null,
  guard?: () => boolean
): Promise<void> {
  // Child first: the outgoing page is unmounted and AWAITED before the
  // layout that hosts it is touched. Idempotent handle — an
  // already-unmounted child is a no-op.
  if (outgoing) {
    await outgoing.unmount();
  }

  // Currency checkpoint between the two teardowns: a navigation that
  // superseded this one during the child's outro owns the document
  // now — its transition plan was made against the live layout, so the
  // layout stays and this exit stops.
  if (guard?.()) return;

  // Layout component teardown: exactly one destructor call, host element
  // removal included (dispose unmounts the component AND removes the
  // runtime-owned [data-go-layout-shell-host]).
  await layout.dispose();

  // Post-exit verification: the layout must be GONE. The marker is the
  // truthful identity (G01); if any element still carries it, the exit
  // did not complete and the document must not be trusted.
  const marked = document.querySelectorAll(
    `[data-go-layout="${CSS.escape(layout.layoutId)}"]`
  );
  if (marked.length > 0) {
    throw new LayoutExitError(
      `layout ${JSON.stringify(layout.layoutId)} still has ${marked.length} marker(s) in the document after exit`
    );
  }
}
