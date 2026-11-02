// Same-origin soft navigation (blueprint section 5): document-level
// anchor-click interception with the exclusion rules of 5.2 step 2.
//
// F05 scope: DECIDE eligibility only. This module never fetches (F06),
// swaps outlets or mounts pages (F07-F08), or touches history (F10).
// When a click is eligible the interceptor calls preventDefault (5.2
// step 3) and hands the navigation off to the registered callback; when
// it is not eligible the browser's native behavior runs untouched
// (binding decision 10: native semantics win).
//
// Not activated from entry.ts yet: no navigation pipeline exists until
// F06+, and preventing default clicks before one does would strand
// users mid-click. Installation is the pipeline's job.

/** An eligible same-origin navigation, handed to the navigate callback. */
export interface LinkNavigation {
  /** Fully resolved absolute URL of the target (same-origin, http/https). */
  url: URL;
  /** Pathname + search + hash — the string history.pushState expects (F10). */
  destination: string;
  /** The anchor that was clicked. */
  anchor: HTMLAnchorElement;
  /** The intercepted click event; its default is already prevented. */
  event: MouseEvent;
}

/** Callback invoked for each eligible click. */
export type NavigateHandler = (navigation: LinkNavigation) => void;

// Single installed state so repeated install calls cannot stack listeners
// (blueprint 5.3: avoid duplicate global listeners, e.g. on HMR).
let activeHandler: NavigateHandler | null = null;
let listenerInstalled = false;

// Capture phase on the document: the outermost listener position, so the
// interceptor sees each click before any bubble-phase handler on inner
// elements can claim or mutate it. In real browsers capture-vs-bubble at
// the document level differs only in ordering relative to other listeners;
// running first is exactly the "not already handled" semantics we want.
// It also sidesteps a happy-dom quirk where unprevented nested-anchor
// clicks navigate mid-dispatch (real browsers never do), which would
// corrupt same-document comparisons made later in propagation.
function onClick(event: MouseEvent): void {
  // Trust: real user clicks report isTrusted === true. Events synthesized
  // by scripts report isTrusted === false and stay native. Environments
  // without trust support (happy-dom) report undefined and pass through.
  if (event.isTrusted === false) {
    return;
  }
  // Non-primary button (middle/right/aux) keeps native behavior.
  if (event.button !== 0) {
    return;
  }
  // Modifier clicks are reserved by the browser: new tab, window, etc.
  if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) {
    return;
  }
  // An earlier handler already claimed this event: leave it untouched.
  if (event.defaultPrevented) {
    return;
  }

  // Delegation from the document root resolves clicks on nested elements
  // inside anchors too (icon, span, em...) back to their <a>.
  const target = event.target instanceof Element ? event.target : null;
  const anchor = target?.closest('a');
  if (!(anchor instanceof HTMLAnchorElement) || !activeHandler) {
    return;
  }

  // Non-current browsing contexts stay native: _blank, _parent, _top,
  // named windows. Keyword matching is ASCII case-insensitive.
  const targetKeyword = anchor.target.toLowerCase();
  if (anchor.hasAttribute('target') && targetKeyword !== '_self') {
    return;
  }
  // Download links always keep the browser's download semantics.
  if (anchor.hasAttribute('download')) {
    return;
  }
  // rel="external" marks links that must leave the SPA.
  if (anchor.rel.split(/\s+/).includes('external')) {
    return;
  }
  // Explicit per-link opt-out (blueprint 5.2 step 2).
  if (anchor.hasAttribute('data-no-spa')) {
    return;
  }

  // Resolve against the document base; unresolvable or invalid URLs stay
  // native rather than risking a wrong navigation.
  let url: URL;
  try {
    url = new URL(anchor.href, document.baseURI);
  } catch {
    return;
  }
  // Only same-origin http(s) documents are soft-navigable; mailto:, tel:,
  // javascript:, data:, and every other scheme stays native.
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    return;
  }
  if (url.origin !== window.location.origin) {
    return;
  }
  // Explicit fragment links keep native scrolling and history semantics.
  // A plain link to the current URL is a no-op; otherwise the browser would
  // reload the document and discard client state. An empty href stays native.
  if (
    url.pathname === window.location.pathname &&
    url.search === window.location.search
  ) {
    const href = anchor.getAttribute('href');
    if (href === null || href === '' || href.includes('#')) return;
    if (window.location.hash === '') {
      event.preventDefault();
      return;
    }
  }

  // Eligible (blueprint 5.2 step 3): take over the default and hand off.
  // The handler owns everything after this point — fetch (F06), render
  // (F07+), history (F10) — and the failure fallback (F12).
  event.preventDefault();
  activeHandler({
    url,
    destination: url.pathname + url.search + url.hash,
    anchor,
    event,
  });
}

/**
 * Install the document-level click interceptor. Idempotent: calling it
 * again replaces the navigate handler and never stacks a second listener.
 *
 * @param onNavigate invoked for every eligible same-origin anchor click.
 */
export function installLinkInterceptor(onNavigate: NavigateHandler): void {
  activeHandler = onNavigate;
  if (!listenerInstalled) {
    document.addEventListener('click', onClick, { capture: true });
    listenerInstalled = true;
  }
}

/**
 * Uninstall the interceptor entirely (tests, teardown). Safe to call when
 * not installed.
 */
export function uninstallLinkInterceptor(): void {
  if (listenerInstalled) {
    document.removeEventListener('click', onClick, { capture: true });
    listenerInstalled = false;
  }
  activeHandler = null;
}
