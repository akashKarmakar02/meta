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
// Layout handling (G04): `env.layouts` describes desired ancestry and does
// not prove DOM instances exist.  The pipeline reads the actually-mounted
// layout stack from layout element markers; entering a CLIENT (.svelte)
// layout subtree mounts it once through frontend/layout-mount.ts and
// hosts the page inside its [data-go-layout-outlet].  Any transition that
// would DISCARD a mounted layout still takes the safe full-document
// fallback — teardown on subtree exit arrives with G07.

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
import { mountComponent } from './svelte-mounter';
import {
  mountedLayoutStack,
  layoutStackTransition,
} from './layout-stack';
import {
  clientLayouts,
  mountPersistentLayout,
  createLayoutPageRoot,
  LayoutMountError,
  type LayoutInstanceHandle,
} from './layout-mount';
import {
  pushNavigationState,
  installPopstateNavigation,
  uninstallPopstateNavigation,
} from './navigation-history';

export { startSoftNavigation, stopSoftNavigation };

let active = false;
let installedPopstate = false;

/** Current CSR page mount handle, owned exclusively by the pipeline. */
let currentPage: CsrPageHandle | null = null;

/**
 * Mounted persistent client layout instance (G04), owned exclusively by
 * the pipeline. Present only when a layout subtree entry has actually
 * completed; the truthful data-go-layout marker in the DOM is what
 * layout-stack's reader sees.
 */
let currentLayout: LayoutInstanceHandle | null = null;

/** Serializes destructive DOM commits (unmount / outlet replace / mount). */
let commitTail: Promise<unknown> = Promise.resolve();

/**
 * Scroll position the pending popstate restoration must apply after its
 * page renders (navigation-history remembers it per destination, F10).
 * Set when popstate fires, consumed (and cleared) inside applyEnvelope.
 */
let popstateScroll: { x: number; y: number } | null = null;

/** Apply a remembered popstate scroll position, if any (§5.2 step 11). */
function restoreScroll(scroll: { x: number; y: number } | null): void {
  if (scroll) window.scrollTo(scroll.x, scroll.y);
}

/** A validated navigation envelope as returned by fetchNavigation. */
type Envelope = Awaited<ReturnType<typeof fetchNavigation>>;

/**
 * Full-document fallback: browser loads the intended destination fresh.
 */
function hardNavigate(url: string): void {
  if (seams.hardNavigate) {
    seams.hardNavigate(url);
    return;
  }
  window.location.assign(url);
}

/**
 * Resolve the generated-registry page component for a CSR envelope.
 * Returns null for unknown entries — callers treat that as a failure and
 * take the hard-navigation fallback.
 */
async function importComponentFor(env: Envelope): Promise<unknown | null> {
  const { loadPage } = await import('./route-import-map');
  return loadPage(env.clientEntry ?? '');
}

/**
 * Mount a CSR page whose envelope just entered a persistent layout (G04):
 * create the runtime-owned page root inside the layout's outlet and mount
 * the already-resolved component there. The caller owns error handling —
 * any throw propagates to the hard-navigation fallback after
 * mountPersistentLayout cleaned up its own partial mount.
 */
async function mountPageInLayout(
  env: Envelope,
  layout: LayoutInstanceHandle,
  component: unknown
): Promise<CsrPageHandle> {
  const pageRoot = createLayoutPageRoot(layout.outlet, env);
  const handle = mountComponent(component as Parameters<typeof mountComponent>[0], { target: pageRoot });
  return { ...handle, pageRoot };
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

    // Layout-shape guard: if this document mounts layouts the destination
    // discards, only a full navigation preserves correct DOM — teardown of
    // persistent layouts on exit is G07, not G04. env.layouts is desired
    // state; DOM markers are actual state.
    const mounted = mountedLayoutStack(document);
    const transition = layoutStackTransition(mounted, env.layouts);
    if (transition.discard.length > 0) {
      hardNavigate(env.url);
      return;
    }

    // G04: client (.svelte) layouts that must ENTER with this navigation.
    // SSR (.templ) enter-list entries belong to documents the server
    // composed — a soft navigation never enters one client-side (the guard
    // above already hard-navigated anything that would need to lose or gain
    // server markup, because direct loads start with it already mounted).
    const enteringLayouts = clientLayouts(transition.enter);

    // Capture the leaving page's scroll BEFORE any teardown: unmounting the
    // outgoing content scrolls the document to the top by itself, so a read
    // taken after would record 0 and back could never restore (step 11).
    const leavingScroll = { x: window.scrollX, y: window.scrollY };

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
      const component = await componentPromise;
      if (!token.isCurrent()) throw new StaleNavigationError(token.id, currentNavigationId());

      if (enteringLayouts.length > 0) {
        // G04 subtree entry: mount the entering client layouts (outermost
        // first — one per chain position today), then host this page inside
        // the innermost layout's outlet. Any failure propagates to the
        // caller's hard-navigation fallback; mountPersistentLayout cleans up
        // its own partial mount first.
        await outgoing?.unmount();
        if (!token.isCurrent()) throw new StaleNavigationError(token.id, currentNavigationId());
        let region: ParentNode;
        if (currentLayout !== null) {
          region = currentLayout.outlet;
        } else {
          const outlets = document.querySelectorAll(PageOutletSelector);
          if (outlets.length !== 1) {
            throw new LayoutMountError(
              `expected exactly one ${PageOutletSelector} to enter a layout subtree, found ${outlets.length}`
            );
          }
          region = outlets[0];
          // Fresh entry from a non-layout document: drop any outgoing SSR
          // page content — the layout subtree replaces the outlet's
          // contents entirely (mountCsrPage does the same for bare pages).
          region.replaceChildren();
        }
        let layoutHandle: LayoutInstanceHandle | null = null;
        for (const layoutId of enteringLayouts) {
          layoutHandle = await mountPersistentLayout(layoutId, region);
          if (!token.isCurrent()) {
            // Superseded mid-entry: tear down what we just mounted so the
            // newer navigation starts from a clean document.
            await layoutHandle.dispose();
            throw new StaleNavigationError(token.id, currentNavigationId());
          }
          region = layoutHandle.outlet;
        }
        currentLayout = layoutHandle;
        if (!currentLayout || !component) {
          // Unknown page component or empty chain: undo the layout entry —
          // the hard fallback reloads the destination fresh anyway.
          const failedLayout = currentLayout;
          currentLayout = null;
          await failedLayout?.dispose();
          throw new LayoutMountError(
            !component
              ? `unknown clientEntry ${JSON.stringify(env.clientEntry)} — not in the generated route registry`
              : 'layout chain produced no mounted instance'
          );
        }
        currentPage = await mountPageInLayout(env, currentLayout, component);
      } else {
        currentPage = await (seams.mountCsr ?? mountCsrPage)(env, outgoing ?? undefined);
      }
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
      (seams.pushState ?? pushNavigationState)(env, { leavingScroll }); // URL + title + scroll reset — post-apply only
    } else {
      document.title = env.title; // popstate owns the history entry already
      restoreScroll(popstateScroll); // step 11: "restore stored scroll positions"
      popstateScroll = null;
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
      // The restored entry's remembered scroll (from the popstate context)
      // is applied by applyEnvelope once the page content it belongs to is
      // actually rendered — never before, or the browser would clamp it
      // against the outgoing page's geometry.
      popstateScroll = ctx.scroll;
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
  if (currentLayout) {
    try {
      await currentLayout.dispose();
    } catch {
      /* already gone */
    }
    currentLayout = null;
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
