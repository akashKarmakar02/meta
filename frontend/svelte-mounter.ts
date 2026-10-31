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
  });
  occupiedTargets.add(target);

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
      }
    },
  };
}
