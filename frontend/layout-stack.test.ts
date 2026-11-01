// G01 tests: layout stack identity and the longest-common-prefix
// transition planner (blueprint task G01; §5.2 steps 7-8).
//
// These are PURE comparison tests plus DOM-marker reading. No mounting,
// no unmounting — that machinery is G02+.

import { describe, it, expect } from 'vitest';
import {
  normalizeLayoutStack,
  layoutStackTransition,
  mountedLayoutStack,
  LayoutStackError,
} from './layout-stack';

describe('normalizeLayoutStack', () => {
  it('trims surrounding whitespace from valid IDs', () => {
    expect(normalizeLayoutStack([' app/layout.templ '])).toEqual([
      'app/layout.templ',
    ]);
  });

  it('rejects blank entries', () => {
    expect(normalizeLayoutStack(['app/layout.templ', '   '])).toBeNull();
    expect(normalizeLayoutStack([''])).toBeNull();
  });

  it('rejects non-string entries', () => {
    expect(normalizeLayoutStack([null])).toBeNull();
    expect(normalizeLayoutStack([42])).toBeNull();
    expect(normalizeLayoutStack([{} as unknown])).toBeNull();
  });

  it('rejects repeated IDs — one ID names one layout instance', () => {
    expect(
      normalizeLayoutStack(['app/layout.templ', 'app/layout.templ'])
    ).toBeNull();
  });

  it('accepts an empty stack (plain page, no layouts)', () => {
    expect(normalizeLayoutStack([])).toEqual([]);
  });
});

describe('layoutStackTransition: longest common prefix', () => {
  // Required case 1: identical stacks return the full shared prefix.
  it('identical stacks retain everything', () => {
    const t = layoutStackTransition(
      ['app/layout.templ'],
      ['app/layout.templ']
    );
    expect(t.retain).toEqual(['app/layout.templ']);
    expect(t.discard).toEqual([]);
    expect(t.enter).toEqual([]);
  });

  it('identical deep stacks retain the whole chain', () => {
    const t = layoutStackTransition(
      ['app/layout.templ', 'app/dashboard/layout.svelte'],
      ['app/layout.templ', 'app/dashboard/layout.svelte']
    );
    expect(t.retain).toEqual([
      'app/layout.templ',
      'app/dashboard/layout.svelte',
    ]);
    expect(t.discard).toEqual([]);
    expect(t.enter).toEqual([]);
  });

  // Required case 2: divergent stacks return the correct shared prefix.
  it('divergent tails split into retain + discard + enter', () => {
    // /dashboard -> /docs (dashboard subtree left, docs subtree entered)
    const t = layoutStackTransition(
      ['app/layout.templ', 'app/dashboard/layout.svelte'],
      ['app/layout.templ', 'app/docs/layout.templ']
    );
    expect(t.retain).toEqual(['app/layout.templ']);
    expect(t.discard).toEqual(['app/dashboard/layout.svelte']);
    expect(t.enter).toEqual(['app/docs/layout.templ']);
  });

  it('retains a prefix of two when only the innermost layout differs', () => {
    const t = layoutStackTransition(
      ['root', 'mid', 'a'],
      ['root', 'mid', 'b']
    );
    expect(t.retain).toEqual(['root', 'mid']);
    expect(t.discard).toEqual(['a']);
    expect(t.enter).toEqual(['b']);
  });

  // Required case 3: root-only common ancestry returns exactly one.
  it('root-only common ancestry retains just the root', () => {
    const t = layoutStackTransition(
      ['app/layout.templ', 'app/dashboard/layout.svelte'],
      ['app/layout.templ', 'app/other/layout.svelte']
    );
    expect(t.retain).toEqual(['app/layout.templ']);
    expect(t.retain).toHaveLength(1);
  });

  // Required case 4: completely disjoint stacks return zero retained.
  it('disjoint stacks retain nothing', () => {
    const t = layoutStackTransition(
      ['a/layout.templ'],
      ['b/layout.templ']
    );
    expect(t.retain).toEqual([]);
    expect(t.discard).toEqual(['a/layout.templ']);
    expect(t.enter).toEqual(['b/layout.templ']);
  });

  // Required case 5: empty stacks are handled on either side.
  it('empty outgoing stack (direct CSR shell) enters everything fresh', () => {
    const t = layoutStackTransition(
      [],
      ['app/layout.templ', 'app/dashboard/layout.svelte']
    );
    expect(t.retain).toEqual([]);
    expect(t.discard).toEqual([]);
    expect(t.enter).toEqual([
      'app/layout.templ',
      'app/dashboard/layout.svelte',
    ]);
  });

  it('empty incoming stack discards everything, innermost first', () => {
    const t = layoutStackTransition(
      ['app/layout.templ', 'app/dashboard/layout.svelte'],
      []
    );
    expect(t.retain).toEqual([]);
    expect(t.discard).toEqual([
      'app/dashboard/layout.svelte',
      'app/layout.templ',
    ]);
    expect(t.enter).toEqual([]);
  });

  it('both empty stacks change nothing', () => {
    const t = layoutStackTransition([], []);
    expect(t.retain).toEqual([]);
    expect(t.discard).toEqual([]);
    expect(t.enter).toEqual([]);
  });

  // Required case 6: order matters, not membership.
  it('same IDs in different positions share nothing (positional compare)', () => {
    // ["a","b"] vs ["b","a"]: "b" nests inside a different parent, so the
    // instances are unrelated despite identical members.
    const t = layoutStackTransition(['a', 'b'], ['b', 'a']);
    expect(t.retain).toEqual([]);
    expect(t.discard).toEqual(['b', 'a']);
    expect(t.enter).toEqual(['b', 'a']);
  });

  it('does not treat a prefix jump as membership overlap', () => {
    // ["a","b"] vs ["x","a"]: "a" appears in both but never at the same
    // position relative to the root, so nothing is reusable.
    const t = layoutStackTransition(['a', 'b'], ['x', 'a']);
    expect(t.retain).toEqual([]);
  });

  it('discards innermost-first so parents outlive children during teardown', () => {
    const t = layoutStackTransition(['r', 'm', 'i'], []);
    expect(t.discard).toEqual(['i', 'm', 'r']);
  });

  it('enters outermost-first so each entering layout has its parent', () => {
    const t = layoutStackTransition([], ['r', 'm', 'i']);
    expect(t.enter).toEqual(['r', 'm', 'i']);
  });

  // Required case 7: repeated IDs / invalid states are rejected.
  it('throws LayoutStackError for repeated IDs in the outgoing stack', () => {
    expect(() =>
      layoutStackTransition(['a', 'a'], ['a'])
    ).toThrow(LayoutStackError);
  });

  it('throws LayoutStackError for repeated IDs in the incoming stack', () => {
    expect(() =>
      layoutStackTransition(['a'], ['a', 'a'])
    ).toThrow(LayoutStackError);
  });

  it('throws LayoutStackError for non-string or blank entries', () => {
    expect(() =>
      layoutStackTransition([null], ['a'])
    ).toThrow(LayoutStackError);
    expect(() =>
      layoutStackTransition(['a'], [''])
    ).toThrow(LayoutStackError);
  });
});

describe('mountedLayoutStack: declared vs mounted', () => {
  // Required case 8: declared ancestry does not imply mounted instances.
  it('returns [] for a document with no layout markers (direct CSR shell)', () => {
    // The bare boot shell Fiber serves for /dashboard declares
    // ["app/layout.templ", "app/dashboard/layout.svelte"] in its envelope
    // but mounts nothing — and this reader must reflect that gap.
    document.body.innerHTML = '<div data-go-page-root></div>';
    expect(mountedLayoutStack(document)).toEqual([]);
  });

  it('reads markers in document order (outermost first)', () => {
    document.body.innerHTML = `
      <div data-go-layout="app/layout.templ">
        <div data-go-layout="app/docs/layout.templ">
          <main>page</main>
        </div>
      </div>`;
    expect(mountedLayoutStack(document)).toEqual([
      'app/layout.templ',
      'app/docs/layout.templ',
    ]);
  });

  it('ignores blank markers', () => {
    document.body.innerHTML = `
      <div data-go-layout="">
        <div data-go-layout="app/layout.templ">x</div>
      </div>`;
    expect(mountedLayoutStack(document)).toEqual(['app/layout.templ']);
  });

  it('observes only; reading never mutates the DOM', () => {
    document.body.innerHTML = `
      <div data-go-layout="app/layout.templ"><p>x</p></div>`;
    const before = document.body.innerHTML;
    mountedLayoutStack(document);
    expect(document.body.innerHTML).toBe(before);
  });

  it('declared-but-unmounted ancestry plans no retention', () => {
    // The composition that matters for G01's contract: envelope says one
    // thing, DOM says another — only the DOM side may be retained.
    document.body.innerHTML = `<div data-go-page-root></div>`;
    const mounted = mountedLayoutStack(document); // []
    const transition = layoutStackTransition(
      mounted,
      ['app/layout.templ', 'app/dashboard/layout.svelte']
    );
    expect(transition.retain).toEqual([]); // nothing mounted to keep
    expect(transition.enter).toEqual([
      'app/layout.templ',
      'app/dashboard/layout.svelte',
    ]);
  });
});
