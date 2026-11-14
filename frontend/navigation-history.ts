// Browser history for soft navigation (blueprint 5.2 steps 10-11).
//
// F10 scope: HISTORY ONLY. This module commits successfully applied
// soft navigations with history.pushState (step 10) and reacts to
// popstate by handing the current URL to a callback WITHOUT creating
// any history entry (step 11). It is deliberately not wired into the
// navigation pipeline — the interceptor still sleeps until F12, and race
// management of the surrounding fetch/mount work is F11's.
//
// Ordering contract (task rule 8 / step 10): the caller applies the
// navigation FIRST (outlet swap or page mount) and only then calls
// pushNavigationState. A failed navigation must never reach this module,
// so history never points at content that was not rendered.

import type { NavigationResponse } from './types';

/** Marker stored in every history entry this framework creates. */
const NAV_STATE_KEY = '__goMetaNavigation';
const INDEX_KEY = '__goMetaIndex';
let currentIndex = 0;
let restoringTraversal = false;

/** Cap on remembered scroll positions (bounded memory, oldest evicted). */
const MAX_SCROLL_MEMORY = 50;

/** Typed failure of a history commit attempt. */
export class HistoryError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'HistoryError';
  }
}

/**
 * Scroll positions per destination, remembered when leaving a page so
 * popstate can restore them ("restore stored scroll positions where
 * possible", step 11).
 */
const scrollMemory = new Map<string, { x: number; y: number }>();

function destinationOf(url: URL): string {
  return url.pathname + url.search + url.hash;
}

function currentDestination(): string {
  return destinationOf(new URL(window.location.href));
}

function rememberScroll(key: string, pos: { x: number; y: number }): void {
  scrollMemory.set(key, pos);
  if (scrollMemory.size > MAX_SCROLL_MEMORY) {
    // Evict the oldest remembered entry.
    const oldest = scrollMemory.keys().next().value;
    if (oldest !== undefined) {
      scrollMemory.delete(oldest);
    }
  }
}

/** Options accepted by pushNavigationState. */
export interface PushNavigationOptions {
  /**
   * Scroll position of the page being LEFT, when the caller captured it
   * before tearing down the outgoing content. Removing page DOM scrolls
   * the document to the top by itself, so remembering at push time reads
   * 0; the pipeline captures the real position before unmount and hands
   * it over here (step 11 restores what the user actually saw).
   */
  leavingScroll?: { x: number; y: number };
}

/**
 * Commit a successfully applied soft navigation to browser history
 * (step 10): push the FINAL resolved URL from the validated envelope —
 * which is where same-origin redirects are reflected — set the page
 * title, and reset scroll (to the anchor when the URL has one, to the
 * top otherwise).
 *
 * Contract:
 * - env must be a validated NavigationResponse (fetchNavigation output),
 *   SSR or CSR mode alike — history does not care which side rendered.
 * - env.url is resolved against the document base and must stay
 *   same-origin; anything else throws HistoryError BEFORE touching
 *   history.
 * - Pushing a destination identical to the current entry replaces it
 *   instead of stacking a duplicate history entry.
 * - The scroll position of the page being left is remembered first, so
 *   a later back/forward can restore it.
 */
export function pushNavigationState(
  env: NavigationResponse,
  options?: PushNavigationOptions
): void {
  if (env == null || typeof env.url !== 'string' || env.url === '') {
    throw new HistoryError(
      'cannot commit navigation: envelope has no url'
    );
  }

  let target: URL;
  try {
    target = new URL(env.url, document.baseURI);
  } catch {
    throw new HistoryError(`envelope url is not parseable: ${env.url}`);
  }
  if (target.origin !== window.location.origin) {
    // Never write foreign URLs into this tab's session history.
    throw new HistoryError(
      `envelope url ${target.href} is not same-origin with ${window.location.origin}`
    );
  }

  const destination = destinationOf(target);

  // Track the document this navigation committed (H09): popstate compares
  // against it to recognize same-document hash traversals.
  currentDocument = { pathname: target.pathname, search: target.search };

  // Remember the scroll of the page being left, under its own key. The
  // caller's pre-teardown capture wins; a fallback read here would see 0
  // whenever the outgoing page was already unmounted (DOM removal scrolls
  // the document to the top on its own).
  rememberScroll(currentDestination(), options?.leavingScroll ?? {
    x: window.scrollX,
    y: window.scrollY,
  });

  if (destination !== currentDestination()) currentIndex++;
  const state = { [NAV_STATE_KEY]: true, [INDEX_KEY]: currentIndex, url: destination };
  if (destination === currentDestination()) {
    // Same destination: replace instead of pushing a duplicate entry.
    window.history.replaceState(state, '', destination);
  } else {
    window.history.pushState(state, '', destination);
  }

  if (typeof env.title === 'string' && env.title !== '') {
    document.title = env.title;
  }

  // Scroll policy (step 10): anchor if the URL names one, top otherwise.
  if (target.hash.length > 1) {
    const anchor = document.getElementById(target.hash.slice(1));
    anchor?.scrollIntoView?.();
  } else {
    window.scrollTo(0, 0);
  }
}

// ---------------------------------------------------------------------------
// popstate (step 11): "fetch the current URL and render without
// pushState; restore stored scroll positions where possible."
//
// Fetching/rendering lives in the navigation pipeline (F11/F12); this
// module owns the listener mechanics: exactly one global listener, no
// history writes, scroll restoration, and handing the resolved current
// URL to the registered callback.

export interface PopstateContext {
  /** The URL the browser restored, same-origin by definition. */
  url: URL;
  /** Scroll position remembered for this destination, if any. */
  scroll: { x: number; y: number } | null;
  /**
   * True when this traversal stayed within the SAME document: the
   * destination's path and query are identical to where we were and only
   * the fragment changed (a fragment-only link's own hash history entry,
   * blueprint 5.2 step 2 — native anchor behavior, not a navigation).
   */
  sameDocument: boolean;
  /** Restore the previous entry if a guard rejects this traversal. */
  cancel(): void;
}

type PopstateHandler = (context: PopstateContext) => void;

let activePopstateHandler: PopstateHandler | null = null;
let popstateListenerInstalled = false;

// The document the SPA is currently showing: path + query of the last
// navigation-committed (or boot) URL. popstate compares against it to
// classify same-document hash traversals.
let currentDocument: { pathname: string; search: string } | null = null;

function onPopstate(): void {
  if (activePopstateHandler === null) {
    return;
  }
  const url = new URL(window.location.href);
  if (restoringTraversal) {
    restoringTraversal = false;
    return;
  }
  const previousDocument = currentDocument;
  const previousIndex = currentIndex;
  const index = window.history.state?.[INDEX_KEY];
  if (typeof index === 'number') currentIndex = index;
  const context: PopstateContext = {
    url,
    scroll: scrollMemory.get(destinationOf(url)) ?? null,
    sameDocument:
      currentDocument !== null &&
      url.pathname === currentDocument.pathname &&
      url.search === currentDocument.search,
    cancel() {
      currentDocument = previousDocument;
      currentIndex = previousIndex;
      if (typeof index === 'number' && index !== previousIndex) {
        restoringTraversal = true;
        window.history.go(previousIndex - index);
      }
    },
  };
  // This traversal becomes the new current document for the next one —
  // including hash-only moves, whose path+search keep it identical.
  currentDocument = { pathname: url.pathname, search: url.search };
  activePopstateHandler(context);
}

/**
 * Register the popstate consumer for the navigation pipeline. Installing
 * twice replaces the previous handler instead of stacking listeners
 * (blueprint 5.3: no duplicate global listeners, e.g. under HMR).
 *
 * The handler must render the given URL WITHOUT calling
 * pushNavigationState — restoration never appends history entries.
 */
export function installPopstateNavigation(handler: PopstateHandler): void {
  activePopstateHandler = handler;
  // Seed the current document from the boot URL so the FIRST popstate can
  // already classify same-document traversals (H09).
  currentDocument = {
    pathname: window.location.pathname,
    search: window.location.search,
  };
  currentIndex = window.history.state?.[INDEX_KEY] ?? 0;
  window.history.replaceState({ ...window.history.state, [INDEX_KEY]: currentIndex }, '', window.location.href);
  restoringTraversal = false;
  if (!popstateListenerInstalled) {
    window.addEventListener('popstate', onPopstate);
    popstateListenerInstalled = true;
  }
}

/** Unregister the popstate consumer; the listener goes dormant. */
export function uninstallPopstateNavigation(): void {
  activePopstateHandler = null;
}
