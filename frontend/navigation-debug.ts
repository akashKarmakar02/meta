// Opt-in navigation debugging: appends one line per pipeline decision to
// window.__goMetaNavLog and (if opened early) console.debug. Enabled ONLY
// by ?go-meta-debug=1 in the URL — zero cost when off, nothing shipped to
// production logs.
//
// Why this exists: G05's real-browser verification initially looked broken
// (layout "remounted") and five debug iterations were spent discovering it
// was the probe, not the pipeline. With decision logging, the same
// investigation is one `window.__goMetaNavLog` read.
//
// Deliberately NOT a test seam: tests stub seams in navigation-pipeline;
// this module never changes behavior, only records it.

export interface NavDecision {
  /** Milliseconds since navigation start. */
  t: number;
  url: string;
  /** Which branch appliedEnvelope took. */
  action:
    | 'retained-child-swap' // G05: layout kept, page swapped
    | 'layout-subtree-entry' // G04: fresh layout mount(s)
    | 'layout-subtree-exit' // G07: tracked layout unmounted on subtree exit
    | 'ssr-outlet-replace' // SSR mode: outlet HTML swap
    | 'hard-navigate-discard' // guard: destination discards a layout the runtime does not track
    | 'stale-navigation'; // superseded mid-flight
  /** Mounted client layouts before the transition. */
  mounted: string[];
  /** Declared client layouts of the destination. */
  declared: string[];
}

declare global {
  interface Window {
    __goMetaNavLog?: NavDecision[];
  }
}

const STORAGE = '__goMetaNavLog';

function enabled(): boolean {
  if (typeof window === 'undefined') return false;
  return new URLSearchParams(window.location.search).get('go-meta-debug') === '1';
}

/**
 * Record one pipeline decision when debug is on. Safe to call from any
 * code path — a disabled probe is a no-op, never a throw.
 */
export function logNavDecision(
  url: string,
  action: NavDecision['action'],
  mounted: readonly string[],
  declared: readonly string[]
): void {
  if (!enabled()) return;
  try {
    const entry: NavDecision = {
      t: Math.round(performance.now()),
      url,
      action,
      mounted: [...mounted],
      declared: [...declared],
    };
    const w = window as unknown as { [k: string]: unknown };
    const log = (w[STORAGE] as NavDecision[] | undefined) ?? (w[STORAGE] = []);
    log.push(entry);
  } catch {
    // Debugging must never break navigation; swallow storage failures.
  }
}
