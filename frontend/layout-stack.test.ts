import { describe, it, expect } from 'vitest';
import {
  normalizeLayoutStack,
  layoutStackTransition,
  mountedLayoutStack,
  LayoutStackError,
} from './layout-stack';

describe('nearest visual layout planning', () => {
  it('accepts no layout or one normalized ID', () => {
    expect(normalizeLayoutStack([])).toEqual([]);
    expect(normalizeLayoutStack([' app/layout.templ '])).toEqual(['app/layout.templ']);
  });

  it('rejects invalid IDs and more than one layout', () => {
    for (const ids of [[''], ['  '], [null], [42], ['a', 'a'], ['a', 'b']]) {
      expect(normalizeLayoutStack(ids)).toBeNull();
    }
    expect(() => layoutStackTransition(['a', 'b'], ['a'])).toThrow(LayoutStackError);
    expect(() => layoutStackTransition(['a'], ['b', 'c'])).toThrow(LayoutStackError);
  });

  it('retains a shared layout', () => {
    expect(layoutStackTransition(['app/dashboard/layout.svelte'], ['app/dashboard/layout.svelte']))
      .toEqual({ retain: ['app/dashboard/layout.svelte'], discard: [], enter: [] });
  });

  it('replaces different layouts without retaining their ancestors', () => {
    expect(layoutStackTransition(['app/dashboard/layout.svelte'], ['app/docs/layout.templ']))
      .toEqual({ retain: [], discard: ['app/dashboard/layout.svelte'], enter: ['app/docs/layout.templ'] });
  });

  it('plans entry, exit, and no-layout transitions', () => {
    expect(layoutStackTransition([], ['a'])).toEqual({ retain: [], discard: [], enter: ['a'] });
    expect(layoutStackTransition(['a'], [])).toEqual({ retain: [], discard: ['a'], enter: [] });
    expect(layoutStackTransition([], [])).toEqual({ retain: [], discard: [], enter: [] });
  });
});

describe('mountedLayoutStack', () => {
  it('does not infer a mounted layout from a bare page root', () => {
    document.body.innerHTML = '<div data-go-page-root></div>';
    const mounted = mountedLayoutStack(document);
    expect(mounted).toEqual([]);
    expect(layoutStackTransition(mounted, ['app/dashboard/layout.svelte']).enter)
      .toEqual(['app/dashboard/layout.svelte']);
  });

  it('reads the actual marker and ignores blank ones without mutating the document', () => {
    document.body.innerHTML = '<div data-go-layout=""><div data-go-layout="app/docs/layout.templ">x</div></div>';
    const before = document.body.innerHTML;
    expect(mountedLayoutStack(document)).toEqual(['app/docs/layout.templ']);
    expect(document.body.innerHTML).toBe(before);
  });

  it('reports multiple markers so an inconsistent document cannot be treated as a valid layout', () => {
    document.body.innerHTML = '<div data-go-layout="a"><div data-go-layout="b"></div></div>';
    expect(() => layoutStackTransition(mountedLayoutStack(document), [])).toThrow(LayoutStackError);
  });
});
