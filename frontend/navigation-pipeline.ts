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
// Layout handling (G04+G05): `env.layouts` describes desired ancestry and
// does not prove DOM instances exist.  The pipeline reads the actually-
// mounted layout stack from layout element markers; entering a CLIENT
// (.svelte) layout subtree mounts it once through frontend/layout-mount.ts
// and hosts the page inside its [data-go-layout-outlet].
//
// G05 child-only swaps: when a CSR navigation's destination keeps every
// MOUNTED client layout (planned via client-layout sequences — the bare
// boot shell mounts no server wrappers, so retention compares only what
// the runtime can own), the retained layout instance keeps its component,
// host, marker, and outlet; only the page inside the outlet is swapped
// (frontend/layout-child-swap.ts).
//
// G07 graceful subtree exit: when the transition DISCARDS the one layout
// instance the pipeline TRACKS, the exit runs soft-side
// (frontend/layout-subtree-exit.ts) — child page first, then the layout
// component, then the host.  A discard naming anything the runtime does
// track NOT (a bare marker, multiple layouts) keeps the safe full-document
// fallback; likewise an SSR destination after exit when the document has
// no [data-go-page-outlet] to render into (a direct-loaded shell — the
// runtime never fabricates server layouts from env.layouts).
//
// The pipeline also ADOPTS the bootstrap's mounted instances (entry.ts)
// so a direct load's layout survives its first soft navigation too: both
// halves of the runtime share one owner instead of two.

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
import { mountComponent, type MountHandle } from './svelte-mounter';
import {
  mountedLayoutStack,
  layoutStackTransition,
  clientLayoutSequence,
} from './layout-stack';
import {
  mountPersistentLayout,
  createLayoutPageRoot,
  LayoutMountError,
  type LayoutInstanceHandle,
} from './layout-mount';
import { swapLayoutChildPage } from './layout-child-swap';
import { unmountLayoutSubtree } from './layout-subtree-exit';
import { logNavDecision } from './navigation-debug';
import { loadLayout } from './route-import-map';
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

/**
 * G05 handover: entry.ts mounts the direct-load document's persistent
 * layout and first page BEFORE the pipeline activates; without handing
 * those instances over, the pipeline's first soft navigation would not
 * know the layout exists and could not retain it (G01: a declared
 * ancestry proves nothing — only tracked instances do).
 *
 * Ownership passes completely: the caller must not use (or unmount) the
 * adopted handles afterwards; adoptMountedInstances is the pipeline's
 * sole initializer for documents whose layout was mounted outside it.
 * The layout must still be live in the document (its marker present);
 * a detached handover is rejected rather than guessed at.
 */
export function adoptMountedInstances(handles: {
  layout: LayoutInstanceHandle;
  page: MountHandle & { readonly pageRoot: HTMLElement };
}): void {
  const marked = document.querySelectorAll(
    `[data-go-layout="${CSS.escape(handles.layout.layoutId)}"]`
  );
  if (marked.length !== 1 || !marked[0].isConnected) {
    throw new LayoutMountError(
      `cannot adopt layout ${JSON.stringify(handles.layout.layoutId)}: no single connected mounted instance in the document`
    );
  }
  currentLayout = handles.layout;
  currentPage = { ...handles.page };
}

/** Transfer a directly mounted page when it has no client layout. */
export function adoptMountedPage(page: MountHandle & { readonly pageRoot: HTMLElement }): void {
  if (!page.pageRoot.isConnected) throw new LayoutMountError('cannot adopt a detached page');
  currentPage = { ...page };
}

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
  // C04: pass the envelope's loader payload (if any) to the page as a prop.
  const handle = mountComponent(component as Parameters<typeof mountComponent>[0], {
    target: pageRoot,
    props: { data: env.data },
  });
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

    // Retention planning on CLIENT (layout.svelte) sequences only (G05).
    // env.layouts is desired state; DOM markers are actual state. Server
    // (.templ) wrappers are dropped from BOTH sides before comparing: the
    // bare CSR boot shell mounts none of them (a direct /dashboard load
    // contains no templ markup), yet its client layouts are as mounted as
    // any other — comparing raw stacks would plan phantom discards and
    // break child-only swaps on direct-loaded shells. Server wrappers the
    // runtime could never unmount anyway are not its to plan.
    const mountedClients = clientLayoutSequence(mountedLayoutStack(document));
    const desiredClients = clientLayoutSequence(env.layouts);
    const transition = layoutStackTransition(mountedClients, desiredClients);

    // G07 graceful subtree exit. discard lists OUTGOING client layouts,
    // innermost first (children die before parents). The pipeline owns at
    // most ONE tracked layout instance today (nested persistent client
    // layouts arrive post-Phase 1), so a discard is executable soft-side
    // only when it names exactly that instance: the mounted-vs-declared
    // distinction made concrete — a marker or an env.layouts entry never
    // proves an unmountable instance exists (requirement 11). Any other
    // discard shape (an untracked marker, more than one layout) keeps the
    // safe full-document fallback: the runtime will not tear down what it
    // does not own.
    let exitingLayout: LayoutInstanceHandle | null = null;
    if (transition.discard.length > 0) {
      const trackedId = currentLayout?.layoutId ?? null;
      const discardMatchesTracked =
        trackedId !== null &&
        transition.discard.length === 1 &&
        transition.discard[0] === trackedId;
      if (discardMatchesTracked && currentLayout !== null) {
        // The one layout the runtime tracks is leaving: execute the G07
        // exit below, then apply the destination normally. The tracked
        // reference stays live until disposal is PROVEN — a superseded
        // exit (child unmounted, guard tripped before layout dispose)
        // hands the still-live handle back below, so the next navigation
        // retains (not duplicates) it and a later exit can dispose it.
        exitingLayout = currentLayout;
      } else {
        logNavDecision(env.url, 'hard-navigate-discard', mountedClients, desiredClients);
        hardNavigate(env.url);
        return;
      }
    }
    const exiting = exitingLayout;

    // G04: client (.svelte) layouts that must ENTER with this navigation.
    // SSR (.templ) enter-list entries belong to documents the server
    // composed — a soft navigation never enters one client-side.
    const enteringLayouts = transition.enter;

    // G05: a mounted, retained client layout hosting the outgoing page —
    // the destination keeps every mounted client layout, and one is
    // currently live (tracked by the pipeline or adopted from bootstrap).
    // This is the mounted-vs-declared distinction made concrete: a layout
    // ID in env.layouts alone never reaches this branch without a real,
    // tracked instance behind it.
    const retainedLayout =
      transition.enter.length === 0 && currentLayout !== null ? currentLayout : null;

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
    // A G07 subtree exit takes this same handle as its CHILD — the layout
    // teardown sequence below unmounts it before its parent.
    const outgoing = currentPage;

    if (env.mode === 'csr') {
      // Import resolved (or failed — caught by the caller's fallback).
      const component = await componentPromise;
      if (!token.isCurrent()) throw new StaleNavigationError(token.id, currentNavigationId());
      if (!component) {
        throw new LayoutMountError(`unknown clientEntry ${JSON.stringify(env.clientEntry)}`);
      }
      for (const layoutId of enteringLayouts) {
        const layoutComponent = await loadLayout(layoutId);
        if (!token.isCurrent()) throw new StaleNavigationError(token.id, currentNavigationId());
        if (!layoutComponent) throw new LayoutMountError(`unknown client layout ${JSON.stringify(layoutId)}`);
      }
      currentPage = null;

      if (exiting !== null) {
        // G07 graceful subtree exit, then bare-page CSR entry: child page
        // unmounted and awaited first, then the layout component disposed
        // (one destructor) and its host removed. The destination applies
        // through mountCsrPage's normal path — it recreates a page root
        // inside the surviving [data-go-page-outlet]. The import resolved
        // BEFORE anything was torn down, so a failed load never stranded
        // the document without its layout... but if teardown or mount
        // throws anyway, the caller's hard-navigation fallback reloads.
        logNavDecision(env.url, 'layout-subtree-exit', mountedClients, desiredClients);
        const superseded = () => !token.isCurrent();
        const exitResult = await unmountLayoutSubtree(exiting, outgoing, superseded);
        if (exitResult.outcome === 'superseded') {
          // Ownership hand-back: the exit stopped at the guard with only
          // the child unmounted — layout A is still live in the document.
          // Restore the authoritative handle so the next navigation retains
          // it (not duplicates it) and a later exit can dispose it exactly
          // once. The stale token then throws as usual: this navigation
          // commits nothing.
          currentLayout = exitResult.layout;
          throw new StaleNavigationError(token.id, currentNavigationId());
        }
        // Disposal proven complete (marker verified gone): drop the tracked
        // reference NOW, before any later await can supersede — otherwise a
        // stale throw would leave currentLayout pointing at a disposed
        // handle and the superseding navigation would treat a dead layout
        // as its retained instance.
        currentLayout = null;
        if (!token.isCurrent()) throw new StaleNavigationError(token.id, currentNavigationId());
      }
      if (retainedLayout !== null && exiting === null) {
        // G05 child-only swap: destination keeps every mounted client
        // layout, so the retained instance keeps its component, host,
        // marker, and outlet; only the page inside the outlet changes.
        // The outgoing page unmounts first, its teardown is awaited, the
        // incoming page mounts into the SAME outlet interior.
        logNavDecision(env.url, 'retained-child-swap', mountedClients, desiredClients);
        currentPage = await swapLayoutChildPage(
          retainedLayout,
          env,
          component,
          outgoing,
          () => {
            if (!token.isCurrent()) throw new StaleNavigationError(token.id, currentNavigationId());
          }
        );
        if (!token.isCurrent()) throw new StaleNavigationError(token.id, currentNavigationId());
      } else if (enteringLayouts.length > 0) {
        logNavDecision(env.url, 'layout-subtree-entry', mountedClients, desiredClients);
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
        if (env.html) {
          await outgoing?.unmount();
          if (!token.isCurrent()) throw new StaleNavigationError(token.id, currentNavigationId());
          const outlet = document.querySelector(PageOutletSelector);
          if (!outlet) throw new LayoutMountError('missing document outlet');
          const template = document.createElement('template');
          template.innerHTML = env.html;
          outlet.replaceChildren(template.content);
        } else if (!exiting) {
          // Discard any outgoing server visual layout before a layout-free page.
          await outgoing?.unmount();
          if (!token.isCurrent()) throw new StaleNavigationError(token.id, currentNavigationId());
          document.querySelector(PageOutletSelector)?.replaceChildren();
        }
        currentPage = await (seams.mountCsr ?? mountCsrPage)(env, undefined);
      }
    } else {
      currentPage = null;
      logNavDecision(env.url, 'ssr-outlet-replace', mountedClients, desiredClients);
      if (exiting !== null) {
        // G07 graceful subtree exit, then SSR outlet replacement: the
        // layout (child page first) tears down completely, and the server's
        // bare page html lands in the document's [data-go-page-outlet].
        // That outlet must exist for the SSR fragment to be renderable —
        // a DIRECT-LOADED shell never mounts one, so this is exactly the
        // case that must fall back to a full document (the server composes
        // layouts; the runtime must never fabricate them from env.layouts).
        const outlets = document.querySelectorAll(PageOutletSelector);
        if (outlets.length !== 1) {
          throw new LayoutMountError(
            `cannot soft-render an SSR page with ${outlets.length} ${PageOutletSelector} elements after layout exit — falling back to full navigation`
          );
        }
        const superseded = () => !token.isCurrent();
        const exitResult = await unmountLayoutSubtree(exiting, outgoing, superseded);
        if (exitResult.outcome === 'superseded') {
          // Ownership hand-back — same rationale as the CSR branch above.
          currentLayout = exitResult.layout;
          throw new StaleNavigationError(token.id, currentNavigationId());
        }
        // Disposal proven complete: drop the tracked reference before any
        // later await can supersede (same rationale as the CSR branch).
        currentLayout = null;
        if (!token.isCurrent()) throw new StaleNavigationError(token.id, currentNavigationId());
        replacePageOutlet(env);
        currentPage = null;
      } else if (outgoing !== null) {
        await outgoing.unmount();
        if (!token.isCurrent()) throw new StaleNavigationError(token.id, currentNavigationId());
        replacePageOutlet(env);
        currentPage = null;
      } else {
        replacePageOutlet(env);
        currentPage = null;
      }
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

    // Only touch the description tag created by Meta. Avoid interpolating
    // server data into selectors or HTML, and commit after the stale check.
    const oldDescription = document.head.querySelector('meta[data-meta-managed="description"]');
    const description = env.metadata?.description;
    if (description) {
      const tag = oldDescription ?? document.createElement('meta');
      tag.setAttribute('name', 'description');
      tag.setAttribute('data-meta-managed', 'description');
      tag.setAttribute('content', description);
      if (!oldDescription) document.head.appendChild(tag);
    } else {
      oldDescription?.remove();
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
    if (err instanceof StaleNavigationError) { // superseded: no-op
      logNavDecision(String(url), 'stale-navigation', [], []);
      return;
    }
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
      // Same-document popstate (a fragment-only link's own hash history
      // entry, per blueprint 5.2 step 2) is native anchor behavior — the
      // interceptor already kept the click native, so there is nothing to
      // render: no fetch, no outlet swap.
      if (ctx.sameDocument) {
        return;
      }
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
