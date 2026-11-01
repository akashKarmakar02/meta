// CSR page mounting (blueprint 5.2 step 9, CSR half): from a validated
// navigation envelope of mode "csr", create/locate the mount target and
// mount the client page component via the E04 lifecycle utility.
//
// F08 scope: mounting only. No history writes (F10), no race
// cancellation (F11), no hard-navigation fallback (F12), no persistent
// layout reconciliation (F09 / Phase G).
//
// DOM ownership rules when entering CSR mode:
// - The envelope's html is empty for CSR (protocol). The page component
//   owns its DOM entirely; nothing from the server is inserted.
// - The mount target is the E10 shell convention: a single element with
//   data-go-page-root. On direct loads entry.ts finds it in the served
//   document; during soft navigation this module looks for it (the
//   previous page may have been CSR and left it in place) or creates it
//   inside the F07 outlet.
// - Entering CSR from an SSR page clears the server-owned outlet content
//   before creating the page root, so SSR remnants can never linger
//   under a mounted Svelte page.

import { hasPage, loadPage } from './route-import-map';
import { mountComponent, type MountHandle } from './svelte-mounter';
import { PageOutletSelector } from './ssr-outlet';
import type { NavigationResponse } from './types';

/** Selector of the CSR mount target (E10 shell convention). */
export const PageRootSelector = '[data-go-page-root]';

/** Typed failure of a CSR mount attempt. */
export class CsrMountError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CsrMountError';
  }
}

/** Handle for one soft-navigation page transition. */
export interface CsrPageHandle extends MountHandle {
  /** The mount-target element this transition used. */
  readonly pageRoot: HTMLElement;
}

/**
 * Mount the CSR page named by the envelope's clientEntry.
 *
 * Contract:
 * - env must be a validated NavigationResponse of mode "csr"
 *   (fetchNavigation output). SSR envelopes are rejected — outlet
 *   replacement is F07's job.
 * - clientEntry must exist in the generated route registry; unknown IDs
 *   throw before any DOM change (the caller decides fallback, F12).
 * - Exactly one mount target rule: an existing [data-go-page-root] is
 *   reused; if none exists, exactly one [data-go-page-outlet] must exist
 *   and a fresh page root is created inside it. Missing/ambiguous
 *   outlets throw CsrMountError.
 * - The old page is unmounted first via its handle (idempotent, awaited
 *   outro transitions per E04); its DOM removal clears stale remnants.
 * - Returns the handle; the caller keeps it for the next transition.
 */
export async function mountCsrPage(
  env: NavigationResponse,
  previous?: MountHandle
): Promise<CsrPageHandle> {
  if (env.mode !== 'csr' || typeof env.clientEntry !== 'string') {
    throw new CsrMountError(
      `CSR mount requires mode "csr" with a clientEntry, got mode ${JSON.stringify(env.mode)}`
    );
  }

  // Resolve the component BEFORE touching the DOM: unknown entries fail
  // without side effects, so callers can fall back safely (F12).
  if (!hasPage(env.clientEntry)) {
    throw new CsrMountError(
      `unknown clientEntry ${JSON.stringify(env.clientEntry)} — not in the generated route registry`
    );
  }
  const component = await loadPage(env.clientEntry);
  if (!component) {
    throw new CsrMountError(
      `failed to load page module for clientEntry ${JSON.stringify(env.clientEntry)}`
    );
  }

  // Unmount the outgoing page first (blueprint 4: unmount outgoing, then
  // mount incoming). Idempotent; awaits outro transitions. A previous
  // handle pointing at a detached element is still fine to clean up.
  if (previous) {
    await previous.unmount();
  }

  // Locate or create the mount target.
  const existing = document.querySelectorAll(PageRootSelector);
  let pageRoot: HTMLElement;
  if (existing.length === 1) {
    pageRoot = existing[0] as HTMLElement;
  } else if (existing.length > 1) {
    throw new CsrMountError(
      `ambiguous mount target: ${existing.length} ${PageRootSelector} elements`
    );
  } else {
    // No page root yet (came from an SSR page): create one inside the
    // F07 outlet, clearing any server-rendered page content that is
    // still there so nothing stale can remain under the Svelte page.
    const outlets = document.querySelectorAll(PageOutletSelector);
    if (outlets.length === 0) {
      throw new CsrMountError(
        `no ${PageOutletSelector} element — cannot create the CSR mount target`
      );
    }
    if (outlets.length > 1) {
      throw new CsrMountError(
        `ambiguous page outlet: ${outlets.length} ${PageOutletSelector} elements`
      );
    }
    const outlet = outlets[0] as HTMLElement;
    outlet.replaceChildren(); // drop the old SSR page content
    pageRoot = document.createElement('div');
    pageRoot.setAttribute('data-go-page-root', '');
    pageRoot.setAttribute('data-go-route', env.url);
    pageRoot.setAttribute('data-go-route-id', env.routeId);
    outlet.appendChild(pageRoot);
  }

  const handle = mountComponent(component, { target: pageRoot });
  return { ...handle, pageRoot };
}
