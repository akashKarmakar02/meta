// SSR page-outlet replacement (blueprint 5.2 step 9): swap the
// server-owned page region with the html fragment from a validated
// NavigationResponse of mode "ssr".
//
// F07 scope: replace ONLY the page outlet, deterministically and
// reversibly. No Svelte mounting (F08), no layout reconciliation (F09 /
// Phase G), no history writes (F10), no race cancellation (F11), no
// hard-navigation fallback (F12).
//
// Trust boundary: env.html arrives from fetchNavigation's validated
// envelope — same-origin server-rendered templ output, never arbitrary
// third-party HTML. Even so, this module inserts it as a document
// fragment without ever executing anything the fragment contains
// (script elements created via innerHTML do not run; no eval path
// exists here).

import type { NavigationResponse } from './types';

/** Selector for the server-owned page region (matches app/layout.templ). */
export const PageOutletSelector = '[data-go-page-outlet]';

/** Typed failure of an outlet replacement attempt. */
export class OutletError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'OutletError';
  }
}

/**
 * Replace the page outlet's children with the envelope's page fragment.
 *
 * Contract:
 * - The envelope must already be a validated NavigationResponse with
 *   mode "ssr" (fetchNavigation's output). CSR envelopes are rejected —
 *   mounting CSR pages is F08's responsibility.
 * - Exactly one [data-go-page-outlet] element must exist in the
 *   document; missing or ambiguous targets throw OutletError before any
 *   DOM change.
 * - The fragment is parsed with <template> and inserted as a node tree:
 *   no duplicate page nodes after repeated replacements, no script
 *   execution (scripts inserted via template content never run).
 * - Only the outlet's children change. Everything outside — root
 *   document structure, head, scripts, event listeners elsewhere — is
 *   preserved untouched.
 *
 * Returns the replaced outlet element.
 */
export function replacePageOutlet(env: NavigationResponse): HTMLElement {
  if (env.mode !== 'ssr') {
    throw new OutletError(
      `outlet replacement requires mode "ssr", got ${JSON.stringify(env.mode)}`
    );
  }

  const outlets = document.querySelectorAll(PageOutletSelector);
  if (outlets.length === 0) {
    throw new OutletError(
      `no ${PageOutletSelector} element in document — cannot replace page`
    );
  }
  if (outlets.length > 1) {
    throw new OutletError(
      `ambiguous page outlet: ${outlets.length} ${PageOutletSelector} elements`
    );
  }
  const outlet = outlets[0] as HTMLElement;

  // Parse server-owned templ output into inert nodes. Template content
  // is a document fragment: <script> tags inside it are never executed,
  // so insertion cannot run response JavaScript.
  const template = document.createElement('template');
  template.innerHTML = env.html;

  // Single atomic mutation: clear the old page, insert the new one. If
  // parsing produced nothing (empty html), the outlet is simply emptied.
  outlet.replaceChildren(template.content);

  return outlet;
}
