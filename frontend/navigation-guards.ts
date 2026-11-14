import type { NavigationAttempt } from './navigation-state';

export interface NavigationGuardContext extends NavigationAttempt {
  readonly signal: AbortSignal;
}
export type NavigationGuard = (context: NavigationGuardContext) => boolean | void | Promise<boolean | void>;
const guards = new Set<NavigationGuard>();

/** Return false to cancel before pending/fetch. Throws are navigation failures. */
export function beforeNavigate(guard: NavigationGuard): () => void {
  guards.add(guard);
  return () => { guards.delete(guard); };
}

/** @internal Snapshot registrations for one attempt. */
export function navigationGuards(): NavigationGuard[] { return [...guards]; }
