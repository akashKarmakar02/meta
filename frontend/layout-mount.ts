// G04: mount a persistent client (layout.svelte) layout ONCE when the
// runtime enters its subtree, and host the current child page inside its
// [data-go-layout-outlet].
//
// Blueprint G04: "Mount dashboard layout once when entering dashboard
// subtree. Done: instrumentation mountCount=1."
//
// This module owns the MOUNTED-layout side of G01's two-stack model:
//
//   ┌───────────────────────────────────────────────────────────────┐
//   │ region ([data-go-page-outlet] after soft entry, or the E10     │
//   │ shell's #app root on a direct load) — runtime-hosted           │
//   │  <div data-go-layout-shell-host>      runtime-owned target     │
//   │    DashboardLayout instance           Svelte-owned             │
//   │      sidebar, counter, [data-go-layout-outlet]                 │
//   │        [data-go-page-root]          runtime-owned interior     │
//   │          child page component        Svelte-owned               │
//   └───────────────────────────────────────────────────────────────┘
//
// Mount counts come from the E04/F09 lifecycle instrumentation
// (onLifecycleEvent / liveInstanceCount), never from module-global
// application state: this module holds no counters of its own.
//
// What belongs to G04 vs later tasks:
// - G04: mounting on subtree entry, the child page hosted inside the
//   outlet, truthful marker, safe failure + partial-failure cleanup.
// - G05/G06: deliberate child-only swap and remount-free return when
//   navigating between sibling pages. (G04 does not build retention
//   machinery; it simply never unmounts a layout whose page changes,
//   because the page mount root lives in the layout's outlet.)
// - G07: unmounting the layout on subtree exit.
// - G08: fresh layout state on re-entry after such an unmount.

import { hasLayout, loadLayout } from './route-import-map';
import { mountComponent, type MountHandle } from './svelte-mounter';
import {
  requireEmptyOutlet,
  LayoutOutletError,
} from './layout-outlet';
import type { NavigationResponse } from './types';

/** Attribute marking the runtime-owned host element a client layout mounts into. */
export const LayoutHostAttr = 'data-go-layout-shell-host';

/** Selector of the runtime-owned host element a client layout mounts into. */
export const LayoutHostSelector = `[${LayoutHostAttr}]`;

/** Typed failure of a persistent-layout mount attempt. */
export class LayoutMountError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'LayoutMountError';
  }
}

/**
 * One mounted persistent client layout and everything it hosts. Owned
 * exclusively by the caller that mounted it (the navigation pipeline /
 * bootstrap) — never stored in module-global state here.
 */
export interface LayoutInstanceHandle extends MountHandle {
  /** The stable layout ID this instance was mounted for. */
  readonly layoutId: string;
  /** The runtime-owned host element (parent of the Svelte root). */
  readonly host: HTMLElement;
  /** The child-content outlet ([data-go-layout-outlet]) inside the layout. */
  readonly outlet: HTMLElement;
  /**
   * Tear the whole subtree down: unmount the component (G03 leaves the
   * outlet element to Svelte's unmount) AND remove the runtime-owned host
   * element. Failure paths use this so a failed subtree entry leaves the
   * document exactly as it was; G07 will use it for graceful exits.
   */
  dispose(): Promise<void>;
}

/**
 * Client layout IDs (layout.svelte) in a declared ancestry, outermost ->
 * innermost. SSR (.templ) layouts appear in ancestry chains but are
 * mounted by the server; only .svelte layouts can mount client-side.
 */
export function clientLayouts(declared: readonly string[]): string[] {
  return declared.filter((id) => hasLayout(id));
}

/**
 * Mount the persistent client layout named by layoutId into `region`
 * (a connected element the runtime hosts the layout subtree in), and
 * return the handle whose `outlet` is where the child page must be
 * mounted.
 *
 * Contract:
 * - The layout ID must exist in the generated layout registry (client
 *   layout.svelte); unknown SSR-only or missing IDs throw before any DOM
 *   change, so callers can take the hard-navigation fallback safely.
 * - A FRESH HOST element ([data-go-layout-shell-host]) is appended to the
 *   region (the region's prior children are NOT touched here — callers
 *   clear outgoing pages first, after unmounting them).
 * - The layout must expose exactly one EMPTY [data-go-layout-outlet]
 *   (G03 contract, enforced by requireEmptyOutlet). Ambiguous or occupied
 *   outlets throw after teardown of the fresh mount.
 * - The layout stamps its own data-go-layout="<layoutId>" marker; the
 *   mount validates it exists exactly once, so the mounted marker always
 *   matches the layout ID used to load the component (env.layouts alone
 *   never proves a mount).
 *
 * Partial-failure cleanup: every throw path after the component mount
 * removes the host element again, so a failed layout entry leaves the
 * region exactly as it was (a caller then hard-navigates).
 */
export async function mountPersistentLayout(
  layoutId: string,
  region: ParentNode
): Promise<LayoutInstanceHandle> {
  // Resolve the component BEFORE touching the DOM (same discipline as
  // csr-mount): unknown IDs fail without side effects.
  const component = await loadLayout(layoutId);
  if (!component) {
    throw new LayoutMountError(
      `unknown or non-client layout ${JSON.stringify(layoutId)} — not in the generated layout registry`
    );
  }

  const host = document.createElement('div');
  host.setAttribute(LayoutHostAttr, '');
  region.appendChild(host);

  let handle: MountHandle;
  try {
    handle = mountComponent(component, { target: host });
  } catch (err) {
    host.remove();
    throw err;
  }

  // Validate the freshly mounted layout against the G03 outlet contract
  // BEFORE declaring it usable. Any violation tears the whole thing down
  // again — the region ends up exactly as it was, with no half-mounted
  // layout left behind.
  let outlet: HTMLElement;
  try {
    outlet = requireEmptyOutlet(host);
  } catch (err) {
    try {
      await handle.unmount();
    } catch {
      /* cleanup must not mask the original failure */
    }
    host.remove();
    if (err instanceof LayoutOutletError) {
      throw new LayoutMountError(
        `mounted layout ${JSON.stringify(layoutId)} violates the outlet contract: ${err.message}`
      );
    }
    throw err;
  }

  // Truthful marker, validated: exactly one element inside the host
  // carrying this layout's ID, matching the ID used to load it.
  const marked = host.querySelectorAll(`[data-go-layout="${CSS.escape(layoutId)}"]`);
  if (marked.length !== 1) {
    try {
      await handle.unmount();
    } catch {
      /* cleanup must not mask the original failure */
    }
    host.remove();
    throw new LayoutMountError(
      `layout ${JSON.stringify(layoutId)} did not stamp its data-go-layout marker exactly once`
    );
  }

  return {
    ...handle,
    layoutId,
    host,
    outlet,
    async dispose(): Promise<void> {
      await handle.unmount();
      host.remove();
    },
  };
}

/**
 * Create the child page mount root inside a mounted layout's outlet.
 *
 * Ownership (G03 boundary): the outlet ELEMENT belongs to the layout; the
 * runtime owns only the interior — exactly the [data-go-page-root] it
 * creates here plus the page component inside that. Any earlier page root
 * in the outlet is replaced wholesale (its component was unmounted by the
 * caller beforehand).
 */
export function createLayoutPageRoot(outlet: HTMLElement, env: NavigationResponse): HTMLElement {
  outlet.replaceChildren(); // runtime-owned interior; fresh page, no remnants
  const pageRoot = document.createElement('div');
  pageRoot.setAttribute('data-go-page-root', '');
  pageRoot.setAttribute('data-go-route', env.url);
  pageRoot.setAttribute('data-go-route-id', env.routeId);
  outlet.appendChild(pageRoot);
  return pageRoot;
}
