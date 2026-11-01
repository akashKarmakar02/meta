// Navigation concurrency coordinator (blueprint 5.3 race safety).
//
// F11 scope: OWNERSHIP + CANCELLATION UTILITIES only. The full pipeline
// assembly (interceptor -> fetch -> apply -> history) lands in F12; this
// module is the small reusable coordinator that assembly plugs in
// without redesigning fetchNavigation, ssr-outlet, csr-mount, or
// navigation-history.
//
// 5.3 rules implemented here:
// - monotonic navigation counter: every beginNavigation() gets a
//   strictly larger id.
// - beginNavigation() aborts the previous navigation's AbortController
//   (cooperative fetch cancellation).
// - abort is not trusted alone: every externally visible commit must pass
//   a token freshness check first, so a stale promise callback that
//   resolved despite cancellation is ignored rather than applied.
// - An intentionally aborted older navigation is not an error requiring
//   fallback; a late-rejected stale request can never trigger one either,
//   because its failure is observed through a stale token.

/** Thrown by assertCurrent when a navigation has been superseded. */
export class StaleNavigationError extends Error {
  /** The superseded navigation's id. */
  readonly id: number;
  /** The id of the navigation that superseded it. */
  readonly supersededBy: number;

  constructor(id: number, supersededBy: number) {
    super(
      `navigation ${id} was superseded by ${supersededBy}; refusing stale commit`
    );
    this.name = 'StaleNavigationError';
    this.id = id;
    this.supersededBy = supersededBy;
  }
}

/**
 * Ownership handle for one soft navigation. Exactly one token is current
 * at any time — the one carrying the highest id ever issued.
 */
export interface NavigationToken {
  /** Monotonic id; strictly increases across beginNavigation() calls. */
  readonly id: number;
  /** Signal wired to this navigation's fetch (and any cancellable work). */
  readonly signal: AbortSignal;
  /**
   * Freshness check: true while this navigation is still the newest.
   * Superseded tokens stay stale forever — ids never rewind.
   */
  isCurrent(): boolean;
  /**
   * Cooperatively cancel this navigation's own request. Aborting a stale
   * token never touches the current navigation's controller.
   */
  abort(): void;
}

let latestNavigationId = 0;
let activeController: AbortController | null = null;

/**
 * Begin a new soft navigation. Aborts the previous navigation's
 * controller (fetch-level cancellation) and returns the new ownership
 * token. The caller threads `token.signal` into fetchNavigation and
 * re-checks `token.isCurrent()` (or asserts current) after EVERY await
 * and before ANY visible commit: DOM replacement, component mount/
 * unmount of incoming pages, history writes, title, or scroll changes.
 */
export function beginNavigation(): NavigationToken {
  // Cancel the superseded navigation's in-flight request first...
  activeController?.abort();
  // ...then issue the new identity. Ids only ever grow, so a token that
  // lost currency can never become current again.
  const id = ++latestNavigationId;
  const controller = new AbortController();
  activeController = controller;

  return {
    id,
    signal: controller.signal,
    isCurrent: () => id === latestNavigationId,
    // A stale token's abort() only re-aborts its own (already aborted)
    // controller; it can never reach the newer navigation's controller
    // held in activeController.
    abort: () => controller.abort(),
  };
}

/** The newest issued navigation id (0 before any navigation began). */
export function currentNavigationId(): number {
  return latestNavigationId;
}

/**
 * Throw StaleNavigationError unless `token` is still current. Use after
 * every await in the navigation pipeline, before committing anything
 * visible. This is the "stale promise callbacks must be ignored" rule
 * made structural: the check cannot be forgotten silently.
 */
export function assertCurrent(token: NavigationToken): void {
  if (!token.isCurrent()) {
    throw new StaleNavigationError(token.id, latestNavigationId);
  }
}

/**
 * Run a commit callback only if `token` is still current. Returns whether
 * the commit ran. For fire-and-forget commits where throwing would be
 * noise; prefer assertCurrent in the main pipeline so staleness is loud.
 */
export function commitIfCurrent(token: NavigationToken, commit: () => void): boolean {
  if (!token.isCurrent()) {
    return false;
  }
  commit();
  return true;
}
