// navigation-pipeline.ts — F12: complete soft-navigation pipeline.
//
// Assembles the Phase F primitives into the blueprint's §5.2 algorithm:
//
//   intercept eligible click → beginNavigation() token → fetchNavigation(url,
//   signal) → validate → apply (SSR outlet replacement | CSR mount via the
//   generated registry) → pushNavigationState (URL + title + scroll) → focus
//   management.  popstate restores without pushing.  Any failure falls back
//   to a full-document navigation to the intended destination; stale/superseded
//   navigations never trigger fallback.
//
// Concurrency model (§5.3 + F11):
//
//   * Fetches and dynamic imports stay concurrent — each navigation fetches
//     and resolves its component independently of any other.
//   * Destructive DOM operations are serialized through a commit queue: only
//     one unmount/replace/mount sequence runs at a time.  Currency is
//     rechecked inside the serialized section, so a superseded navigation can
//     never interleave its teardown into a newer transition — it exits as a
//     StaleNavigationError before touching the DOM.
//   * Token currency is re-verified after every await (fetch resolve, import
//     resolve, unmount settle) — a stale result throws before any externally
//     visible commit of DOM, title, history, or scroll.
//   * Hard-navigation fallback runs only for the current navigation: stale
//     or intentionally aborted transitions exit as no-ops.
//
// Layout limitation (documented per F12 requirements): `env.layouts` describes
// desired ancestry and does not prove DOM instances exist.  The pipeline reads
// the actually-mounted layout stack from layout element markers and soft-
// navigates only when the destination's desired stack keeps every mounted
// layout in place; any other shape change (adding or removing a nested
// layout, e.g. / → /docs) uses the safe full-document fallback rather than
// producing a mismatched DOM.  Full client-side layout reconciliation is
// Phase G (G01/G02).

import { installLinkInterceptor, uninstallLinkInterceptor } from './router';
import { fetchNavigation, NavigationError } from './navigation';
import {
  beginNavigation,
  currentNavigationId,
  StaleNavigationError,
  type NavigationToken,
} from './navigation-coordinator';
import { replacePageOutlet, PageOutletSelector } from './ssr-outlet';
import { mountCsrPage, type CsrPageHandle } from './csr-mount';
import {
  pushNavigationState,
  installPopstateNavigation,
  uninstallPopstateNavigation,
} from './navigation-history';

export { startSoftNavigation, stopSoftNavigation };

/** Element marking an SSR layout instance, carrying its layout ID. */
const LAYOUT_ATTR = 'data-go-layout';

let active = false;
let installedPopstate = false;

/** Current CSR page mount handle, owned exclusively by the pipeline. */
let currentPage: CsrPageHandle | null = null;

/** Serializes destructive DOM commits (unmount / outlet replace / mount). */
let commitTail: Promise<unknown> = Promise.resolve();

/** A validated navigation envelope as returned by fetchNavigation. */
type Envelope = Awaited<ReturnType<typeof fetchNavigation>>;

/**
 * Layout IDs actually present in the document, outermost first.
 * Derived from DOM markers, never from env.layouts alone.
 */
function mountedLayoutStack(doc: Document): string[] {
  const stack: string[] = [];
  for (const el of Array.from(doc.querySelectorAll(`[${LAYOUT_ATTR}]`))) {
    const id = el.getAttribute(LAYOUT_ATTR);
    if (id && id.trim() !== '') stack.push(id);
  }
  return stack;
}

/**
 * True when every currently-mounted layout also appears at the same position
 * in the target's desired ancestry — i.e. the soft transition only swaps page
 * content inside already-mounted layout shells.  Anything else (a nested
 * layout the current document lacks, or one it must lose) requires the full
 * document.
 */
function mountedStackWithinDesired(mounted: string[], desired: string[]): boolean {
  if (mounted.length === 0) return true; // plain document: outlet swap / fresh mount
  if (mounted.length > desired.length) return false;
  for (let i = 0; i < mounted.length; i++) {
    if (mounted[i] !== desired[i]) return false;
  }
  return true;
}

/** Full-document fallback: browser loads the intended destination fresh. */
function hardNavigate(url: string): void {
  if (seams.hardNavigate) {
    seams.hardNavigate(url);
    return;
  }
  window.location.assign(url);
}

/** Resolve the generated registry component for a CSR envelope. */
async function importComponentFor(env: Envelope): Promise<void> {
  const { loadPage } = await import('./route-import-map');
  await loadPage(env.clientEntry ?? '');
}

/** Focus the primary content region without scrolling (§5.4). */
function focusMainRegion(): void {
  const main =
    document.querySelector('[data-go-page-root]') ??
    document.querySelector(PageOutletSelector);
  if (main instanceof HTMLElement) {
    main.setAttribute('tabindex', '-1');
    main.focus({ preventScroll: true });
  }
}

/**
 * Render a validated envelope into the document.  Serialized against other
 * destructive commits; currency rechecked after each await.  `push` controls
 * whether a new history entry is written (link clicks) or the existing entry
 * is kept (popstate restoration).
 */
async function applyEnvelope(
  token: NavigationToken,
  env: Envelope,
  push: boolean,
): Promise<void> {
  if (!token.isCurrent()) throw new StaleNavigationError(token.id, currentNavigationId());

  const run = async (): Promise<void> => {
    if (!token.isCurrent()) throw new StaleNavigationError(token.id, currentNavigationId());

    // Layout-shape guard: if this document mounts nested SSR layouts that the
    // destination does not keep identical, only a full navigation preserves
    // correct DOM. env.layouts is desired state; DOM markers are actual state.
    const mounted = mountedLayoutStack(document);
    if (!mountedStackWithinDesired(mounted, env.layouts)) {
      hardNavigate(env.url);
      return;
    }

    // Resolve the CSR component (dynamic import) BEFORE unmounting anything:
    // a failed import must leave the current page intact for fallback.
    const componentPromise =
      env.mode === 'csr' ? importComponentFor(env) : null;

    // Tear down any outgoing CSR page first; mountCsrPage owns the unmount
    // (it awaits the outgoing handle's outro before mounting the new page).
    const outgoing = currentPage;
    currentPage = null;

    if (env.mode === 'csr') {
      // Import resolved (or failed — caught by the caller's fallback).
      await componentPromise;
      if (!token.isCurrent()) throw new StaleNavigationError(token.id, currentNavigationId());
      currentPage = await (seams.mountCsr ?? mountCsrPage)(env, outgoing ?? undefined);
    } else {
      if (outgoing !== null) await outgoing.unmount();
      if (!token.isCurrent()) throw new StaleNavigationError(token.id, currentNavigationId());
      replacePageOutlet(env);
      currentPage = null;
    }
    // Currency after the last await: a superseding navigation that began during
    // our mount/unmount makes us stale before history commits.
    if (!token.isCurrent()) throw new StaleNavigationError(token.id, currentNavigationId());

    if (push) {
      (seams.pushState ?? pushNavigationState)(env); // URL + title + scroll reset — post-apply only
    } else {
      document.title = env.title; // popstate owns the history entry already
    }

    focusMainRegion();
  };

  // Serialize destructive sections; keep the tail alive regardless of outcome.
  const runP = commitTail.then(run, run);
  commitTail = runP.then(
    () => undefined,
    () => undefined,
  );
  await runP;
}

/**
 * One soft navigation attempt.  Shared by link clicks (push: true) and
 * popstate restoration (push: false).
 */
export async function navigateTo(url: string, opts?: { push?: boolean }): Promise<void> {
  const push = opts?.push !== false;
  const token = beginNavigation();
  try {
    const env = await doFetch(url, token.signal);
    if (!token.isCurrent()) throw new StaleNavigationError(token.id, currentNavigationId());
    await applyEnvelope(token, env, push);
  } catch (err) {
    if (err instanceof StaleNavigationError) return; // superseded: no-op
    if (isAbortError(err)) return; // aborted by newer navigation: no-op
    if (!token.isCurrent()) return; // late failure of a stale request: no fallback
    // Current navigation failed (network error, invalid protocol response,
    // missing component, failed transition): full-document fallback to the
    // intended destination so the user is never stranded on stale content.
    hardNavigate(url);
  }
}

/** Abort errors surface as TypeError/DOMException with name 'AbortError'. */
function isAbortError(err: unknown): boolean {
  return (
    err instanceof Error &&
    (err.name === 'AbortError' ||
      (err instanceof TypeError && /abort/i.test(String(err))))
  );
}

export interface SoftNavigationOptions {
  /** Reserved for future use. */
  root?: Document;
}

/** Activate the soft-navigation pipeline. Idempotent. */
function startSoftNavigation(_opts?: SoftNavigationOptions): void {
  if (active) return;
  active = true;

  installLinkInterceptor(async (nav) => {
    await navigateTo(nav.destination || nav.url.toString(), { push: true });
  });

  if (!installedPopstate) {
    installedPopstate = true;
    installPopstateNavigation((ctx) => {
      void navigateTo(ctx.url.toString(), { push: false });
    });
  }
}

/** Deactivate the pipeline (tests). */
function stopSoftNavigation(): void {
  if (!active) return;
  active = false;
  uninstallLinkInterceptor();
  if (installedPopstate) {
    installedPopstate = false;
    uninstallPopstateNavigation();
  }
}

// NavigationError is re-checked implicitly: every fetch failure path routes
// through navigateTo's catch, which distinguishes stale from real failures.
void NavigationError;

// ---- test seams (used only by navigation-pipeline.test.ts) ----

/** Overridable seams injected per-test. */
type PipelineSeams = {
  fetch: typeof fetchNavigation | null;
  mountCsr: typeof mountCsrPage | null;
  pushState: typeof pushNavigationState | null;
  hardNavigate: ((url: string) => void) | null;
};

const seams: PipelineSeams = {
  fetch: null,
  mountCsr: null,
  pushState: null,
  hardNavigate: null,
};

/**
 * Replace pipeline dependencies for testing.  Pass null for each seam to
 * restore the real implementation.  Test-only — never imported by production
 * code paths.
 */
export function __setHandlesForTesting(overrides: Partial<PipelineSeams>): void {
  Object.assign(seams, overrides);
}

/**
 * Test-only: reset pipeline state (mounted handle, active token) between
 * tests.  Production code must never call this.
 */
export async function __resetPipelineForTesting(): Promise<void> {
  if (currentPage) {
    try {
      await currentPage.unmount();
    } catch {
      /* already gone */
    }
    currentPage = null;
  }
}

// The internal call sites route through indirections so tests can intercept.
// The URL is normalized exactly like the real fetchNavigation would (absolute,
// fragment-stripped) so a test seam sees the same argument production code
// sends over the wire.
function doFetch(url: string, signal?: AbortSignal): Promise<Envelope> {
  const requestURL = new URL(url, document.baseURI);
  requestURL.hash = '';
  return (seams.fetch ?? fetchNavigation)(requestURL.href, { signal });
}
