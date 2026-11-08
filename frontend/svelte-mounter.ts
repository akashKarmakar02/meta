// General-purpose Svelte 5 component lifecycle utility.
//
// Wraps Svelte 5's official mount()/unmount() so the rest of the client
// runtime (entry.ts today, the navigation runtime in later phases) never
// calls them directly. Route selection — deciding WHICH component belongs
// on the page — lives in the bootstrap/import map/registry, never here.
import { mount as svelteMount, unmount as svelteUnmount } from 'svelte';
import type { Component } from 'svelte';

/** Options accepted by mountComponent. */
export interface MountOptions {
  /** Element the component is mounted into. Must be connected by the caller. */
  target: Element;
  /** Optional props passed to the component. */
  props?: Record<string, any>;
  /** Svelte context inherited from a layout outlet. */
  context?: Map<any, any>;
}

/**
 * Cleanup handle for one mounted component instance. Keep the instance
 * itself opaque; callers interact through this interface only.
 */
export interface MountHandle {
  /** The element this instance was mounted into. */
  readonly target: Element;
  /**
   * True from mount until the unmount promise settles — during a pending
   * outro transition the instance is still mounted, and the target is
   * still occupied.
   */
  readonly mounted: boolean;
  /**
   * Unmount the instance. Returns Svelte's own unmount promise: cleanup
   * is complete only once it resolves (i.e. after outro transitions have
   * finished). Idempotent — calling it again after completion is a no-op.
   */
  unmount(options?: { outro?: boolean }): Promise<void>;
}

// Targets with a live (or still outroing) instance. Keyed weakly so the
// map never keeps detached elements alive.
const occupiedTargets = new WeakSet<Element>();

// ---------------------------------------------------------------------------
// F09 lifecycle instrumentation: stable observability of mount/unmount.
// Purely additive — the runtime behaves identically with zero listeners,
// and listeners can never break a transition (their throws are contained).

/** One observed lifecycle transition of a mounted instance. */
export interface LifecycleEvent {
  /** What happened. */
  phase: 'mounted' | 'unmounted';
  /** The target element the instance is (or was) mounted into. */
  target: Element;
}

type LifecycleListener = (event: LifecycleEvent) => void;

const lifecycleListeners = new Set<LifecycleListener>();

/**
 * Live instance count: incremented once per successful mount, decremented
 * once per completed unmount. With correct usage (every mount eventually
 * unmounted) it returns to 0 — the stability signal F09 pins in tests.
 */
let liveInstances = 0;

/** Observe every mount/unmount. Returns an unsubscribe function. */
export function onLifecycleEvent(listener: LifecycleListener): () => void {
  lifecycleListeners.add(listener);
  return () => {
    lifecycleListeners.delete(listener);
  };
}

/** Number of currently mounted (or still outroing) instances. */
export function liveInstanceCount(): number {
  return liveInstances;
}

function emitLifecycle(event: LifecycleEvent): void {
  // Copy: a listener unsubscribing mid-emit must not skip the others.
  for (const listener of [...lifecycleListeners]) {
    try {
      listener(event);
    } catch {
      // Instrumentation must never take down the navigation runtime.
    }
  }
}

/**
 * Mount a component into a target element.
 *
 * Repeated mounting into an already-occupied target is rejected: it would
 * leak duplicate instances, and the navigation runtime must unmount the
 * outgoing page first (blueprint §4: unmount outgoing components, then
 * mount the incoming page). After unmount resolves, the target is free
 * again.
 */
export function mountComponent(
  component: Component<any, any, any>,
  options: MountOptions
): MountHandle {
  const target = options?.target;
  if (!target) {
    throw new Error('svelte-mounter: mount requires a target element');
  }
  if (occupiedTargets.has(target)) {
    throw new Error('svelte-mounter: target already has a mounted component; unmount it first');
  }

  const instance = svelteMount(component, {
    target,
    ...(options.props ? { props: options.props } : {}),
    ...(options.context ? { context: options.context } : {}),
  });
  occupiedTargets.add(target);
  liveInstances++;
  emitLifecycle({ phase: 'mounted', target });

  let active = true;
  return {
    target,
    get mounted(): boolean {
      return active;
    },
    unmount: async (unmountOptions = {}) => {
      if (!active) {
        return;
      }
      active = false;
      try {
        await svelteUnmount(instance, unmountOptions);
      } finally {
        // Free the target whether Svelte's cleanup finished cleanly or
        // threw: a poisoned slot would break every later mount more than
        // reporting the original failure does.
        occupiedTargets.delete(target);
        liveInstances--;
        emitLifecycle({ phase: 'unmounted', target });
      }
    },
  };
}
