/** Framework-neutral soft-navigation observation. No DOM or Svelte dependency. */
export interface NavigationAttempt {
  readonly id: number;
  readonly url: string;
  readonly source: 'link' | 'programmatic' | 'history';
}

export type NavigationOutcome =
  | { readonly type: 'completed' }
  | { readonly type: 'canceled'; readonly reason: 'guard' | 'aborted' | 'superseded' | 'stopped' }
  | { readonly type: 'failed'; readonly error: unknown }
  | { readonly type: 'fallback' };

export type NavigationState =
  | { readonly status: 'pending'; readonly attempt: NavigationAttempt; readonly outcome: null }
  | { readonly status: 'idle'; readonly attempt: NavigationAttempt | null; readonly outcome: NavigationOutcome | null };

type Listener = (state: NavigationState) => void;
let state: NavigationState = Object.freeze({ status: 'idle', attempt: null, outcome: null });
let revision = 0;
const listeners = new Map<Listener, number>();
const notifications: Array<{ snapshot: NavigationState; revision: number }> = [];
let notifying = false;

export function getNavigationState(): NavigationState { return state; }

/** Immediately receives the snapshot, then each change. Always unsubscribe on teardown. */
export function subscribeNavigation(listener: Listener): () => void {
  listeners.set(listener, revision);
  notify(listener, state);
  return () => { listeners.delete(listener); };
}

function notify(listener: Listener, snapshot: NavigationState): void {
  try { listener(snapshot); } catch { /* Observers cannot break navigation. */ }
}

/** @internal Pipeline owner only. Older/duplicate terminal writes are ignored. */
export function publishNavigation(attempt: NavigationAttempt, outcome: NavigationOutcome | null): void {
  if (state.attempt && (attempt.id < state.attempt.id ||
    (attempt.id === state.attempt.id && state.status === 'idle'))) return;
  state = Object.freeze(outcome
    ? { status: 'idle', attempt, outcome: Object.freeze(outcome) }
    : { status: 'pending', attempt, outcome: null });
  notifications.push({ snapshot: state, revision: ++revision });
  // Queue reentrant emissions so every subscriber observes changes in order.
  if (notifying) return;
  notifying = true;
  try {
    while (notifications.length) {
      const notification = notifications.shift()!;
      for (const [listener] of [...listeners]) {
        // A subscriber joining during delivery already received the latest
        // snapshot; do not replay older queued changes or duplicate it.
        const delivered = listeners.get(listener);
        if (delivered !== undefined && delivered < notification.revision) {
          listeners.set(listener, notification.revision);
          notify(listener, notification.snapshot);
        }
      }
    }
  } finally { notifying = false; }
}
