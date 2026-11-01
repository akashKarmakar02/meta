// G03: the child-content outlet contract inside persistent Svelte layouts.
//
// Blueprint G03: "Provide explicit content outlet inside persistent Svelte
// layout. Done: sub-page can mount without replacing layout DOM."
//
// OWNERSHIP BOUNDARY (the whole point of this module):
//
//   ┌──────────────────────────────────────────────────────────────┐
//   │ layout component (Svelte-owned)                              │
//   │  sidebar, counter, content wrapper                            │
//   │  ┌────────────────────────────────────────────────────────┐ │
//   │  │ [data-go-layout-outlet]          Svelte owns the ELEMENT │ │
//   │  │  ┌──────────────────────────────────────────────────┐  │ │
//   │  │  │ outlet interior        NAVIGATION RUNTIME owned  │  │ │
//   │  │  │ (page mount root + mounted page component)      │  │ │
//   │  │  └──────────────────────────────────────────────────┘  │ │
//   │  └────────────────────────────────────────────────────────┘ │
//   └──────────────────────────────────────────────────────────────┘
//
// - Svelte owns the outlet element and everything outside it. Unmounting
//   the layout removes the outlet element together with the layout.
// - The navigation runtime owns the outlet's interior exclusively: it
//   creates the page mount root inside the outlet (G04+) and must unmount
//   that page instance before unmounting the layout.
// - Because a persistent Svelte parent must never be destroyed by child
//   activity, runtime code NEVER replaces the outlet element itself —
//   only its children. locateLayoutOutlets exists so the runtime can find
//   the outlet without owning it.
//
// This module observes only; it does not mount or unmount anything.

/** Selector of a persistent layout's child-content outlet. */
export const LayoutOutletSelector = '[data-go-layout-outlet]';

/**
 * The designated outlets in a document, outermost first.
 * Order mirrors G01's mounted-layout-stack convention so callers can
 * correlate an outlet with its layout instance.
 */
export function locateLayoutOutlets(root: ParentNode): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(LayoutOutletSelector));
}

/**
 * True when `el` sits at or inside one of the document's layout outlets —
 * i.e. the navigation runtime owns it (or may replace it) under the G03
 * boundary. Used by the runtime to decide which nodes it may touch.
 */
export function isInsideLayoutOutlet(el: Node | null): boolean {
  if (!el || !el.isConnected) return false;
  const outlet = el instanceof Element ? el.closest(LayoutOutletSelector) : null;
  if (outlet) return true;
  // A detached-but-once-inside node cannot be answered with closest();
  // the runtime only asks about connected nodes.
  return false;
}

/**
 * Validate that a layout instance can host a page mount: exactly one
 * outlet under it, empty of ELEMENT children (no leftover children from
 * an earlier snippet render or mount). Returns the outlet, or throws
 * when the contract is violated — callers treat that as a protocol
 * error, never a guess.
 *
 * "Empty" deliberately means no elements: Svelte may keep internal anchor
 * nodes (comments/whitespace from {@render ...}) inside the outlet; those
 * belong to Svelte, not the runtime, and must not block a mount.
 */
export function requireEmptyOutlet(layoutEl: ParentNode): HTMLElement {
  const outlets = locateLayoutOutlets(layoutEl);
  if (outlets.length === 0) {
    throw new LayoutOutletError(
      `layout ${describe(layoutEl)} has no ${LayoutOutletSelector} child outlet`
    );
  }
  if (outlets.length > 1) {
    throw new LayoutOutletError(
      `layout ${describe(layoutEl)} has ${outlets.length} child outlets — expected exactly one`
    );
  }
  const outlet = outlets[0];
  if (outlet.firstElementChild !== null) {
    throw new LayoutOutletError(
      `outlet in ${describe(layoutEl)} is not empty — children-snippet renders and runtime mounts are mutually exclusive`
    );
  }
  return outlet;
}

/** Violation of the layout-outlet ownership contract. */
export class LayoutOutletError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'LayoutOutletError';
  }
}

function describe(el: ParentNode): string {
  if (el instanceof Element) {
    const id = el.getAttribute('data-go-layout') ?? '';
    return id !== '' ? id : el.tagName.toLowerCase();
  }
  return String(el);
}
