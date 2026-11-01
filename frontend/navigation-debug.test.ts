// navigation-debug tests: the opt-in decision log never changes behavior,
// records only when ?go-meta-debug=1, and never throws.
import { describe, it, expect, beforeEach } from 'vitest';
import { logNavDecision } from './navigation-debug';

describe('navigation-debug (?go-meta-debug=1)', () => {
  beforeEach(() => {
    window.history.replaceState(null, '', '/');
    delete (window as unknown as Record<string, unknown>).__goMetaNavLog;
  });

  it('is a no-op when the query param is absent', () => {
    logNavDecision('/x', 'retained-child-swap', ['a.svelte'], ['a.svelte']);
    expect((window as unknown as Record<string, unknown>).__goMetaNavLog).toBeUndefined();
  });

  it('appends one entry per decision when enabled', () => {
    window.history.replaceState(null, '', '/?go-meta-debug=1');
    logNavDecision('/a', 'retained-child-swap', ['l.svelte'], ['l.svelte']);
    logNavDecision('/b', 'hard-navigate-discard', ['l.svelte'], []);
    const log = (window as unknown as { __goMetaNavLog?: Array<Record<string, unknown>> }).__goMetaNavLog;
    expect(log).toHaveLength(2);
    expect(log![0]).toMatchObject({ url: '/a', action: 'retained-child-swap' });
    expect(log![1]).toMatchObject({ url: '/b', action: 'hard-navigate-discard' });
    // Snapshot of the stacks, order preserved.
    expect(log![0].mounted).toEqual(['l.svelte']);
    expect(log![1].declared).toEqual([]);
  });
});
