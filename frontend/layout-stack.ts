// Layout stack identity and persistence planning (blueprint §4.3, §5.2
// steps 7-8; task G01).
//
// A route declares zero or one nearest visual-layout ID. These helpers
// compare mounted client instances with that declaration. The generic
// sequence operations can also detect inconsistent mounted states.
//
// Two DIFFERENT kinds of stacks exist, and conflating them is the classic
// bug this module exists to prevent:
//
// - DECLARED layout: what route metadata says SHOULD wrap a page. It is
//   not proof that a client instance has mounted.
// - MOUNTED ancestry: what layout INSTANCES actually exist in the current
//   document, read from DOM markers ([data-go-layout] elements, emitted by
//   templ layouts) plus, from G02 on, client-side layout instance records.
//   Only a mounted instance can be RETAINED; an ID appearing in metadata
//   proves nothing.
//
// G01 scope: the PURE comparison model only. Deciding which instances to
// unmount/mount with this model (G03+), Svelte layout components (G02),
// and mounting machinery all come later; nothing in this file touches the
// DOM except the mounted-stack READER, which only observes.

/** One layout's stable file-identity, e.g. "app/dashboard/layout.svelte". */
export type LayoutId = string;

/** Marker attribute templ layouts carry on their wrapper element. */
const LAYOUT_MARKER = 'data-go-layout';

// G05: file extension decides who can mount a layout instance. A .svelte
// layout is a CLIENT component the runtime mounts/unmounts; a .templ
// layout is SERVER markup that arrives composed inside a document (or is
// simply absent — the bare CSR boot shell contains no templ markup at
// all). Retention planning compares CLIENT layouts only (G10 rejects
// mixed hierarchies, so in every valid stack the client entries form a
// contiguous suffix of the chain); server wrappers around them can never
// be unmounted client-side, so they prove nothing about retention.
const CLIENT_LAYOUT_EXT = '.svelte';

/**
 * True when the layout ID names a CLIENT (layout.svelte) component — the
 * only kind the navigation runtime can mount, retain, or unmount.
 * Purely structural (the ID's extension); no registry lookup here.
 */
export function isClientLayoutId(id: LayoutId): boolean {
  return id.endsWith(CLIENT_LAYOUT_EXT);
}

/**
 * The CLIENT (layout.svelte) subsequence of a stack, order preserved.
 *
 * G05's retention comparison base: layoutStackTransition over these
 * subsequences plans mount/retain/unmount of exactly the instances the
 * runtime owns. Server (.templ) entries are dropped because the bare
 * CSR boot shell never mounts them, yet its client layouts are as
 * mounted as any other — comparing unfiltered stacks against such a
 * document would plan phantom discards of layouts that are alive and
 * should be retained (the positional shift the missing server prefix
 * causes is not a real hierarchy change).
 */
export function clientLayoutSequence(stack: readonly LayoutId[]): LayoutId[] {
  return stack.filter(isClientLayoutId);
}

/** Invalid layout-chain input (malformed stack, duplicate IDs). */
export class LayoutStackError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'LayoutStackError';
  }
}

/**
 * Validate a raw layout-ID sequence into a usable stack.
 *
 * Rules (blueprint §4.3: hierarchies not explicitly defined are a
 * deliberate restriction, surfaced as an error — never a silent guess):
 * - every entry must be a string (after trimming) naming a file path;
 * - blank/whitespace-only entries are invalid;
 * - there may be at most one ID, because a route selects one visual layout.
 *
 * Returns a new array of trimmed IDs, or null when the input is not a
 * valid stack.
 */
export function normalizeLayoutStack(
  ids: readonly unknown[]
): LayoutId[] | null {
  if (ids.length > 1) return null;
  const out: LayoutId[] = [];
  for (const id of ids) {
    if (typeof id !== 'string') return null;
    const trimmed = id.trim();
    if (trimmed === '') return null;
    if (out.includes(trimmed)) return null; // repeated ID: ambiguous chain
    out.push(trimmed);
  }
  return out;
}

/**
 * What to do with layout instances when navigating from one stack to
 * another. All three lists hold layout IDs; `retain` keeps outermost-first
 * order, the other two are ordered for their operation (below).
 */
export interface LayoutStackTransition {
  /**
   * Longest common prefix of the two stacks, outermost first. These are
   * the ONLY instances allowed to survive the navigation; their state
   * (e.g. a Svelte layout counter) persists untouched.
   */
  readonly retain: readonly LayoutId[];
  /**
   * Outgoing-only layouts to UNMOUNT, INNERMOST FIRST: a parent cannot be
   * torn down while its child still lives inside it, so children go before
   * the wrappers that contained them.
   */
  readonly discard: readonly LayoutId[];
  /**
   * Incoming-only layouts to MOUNT, OUTERMOST FIRST: each entering layout
   * needs its parent (deepest retained or otherwise entering) to exist
   * before it can attach.
   */
  readonly enter: readonly LayoutId[];
}

/**
 * Plan the transition between two layout stacks.
 *
 * The comparison is POSITIONAL, pair by pair from the outside in — never
 * by set membership. Stacks ["a","b"] and ["b","a"] share NOTHING even
 * though they contain the same IDs: "b" sits inside different parents, so
 * the "b" instances are unrelated and must swap. This positional rule is
 * the "same root chain => reusable" requirement made exact.
 *
 * Cases (all handled by the same loop):
 * - identical stacks        -> retain = whole stack, no discard, no enter;
 * - divergent stacks        -> retain = shared prefix, both tails planned;
 * - root-only common part   -> retain = ["<root>"] (one entry);
 * - disjoint stacks         -> retain = [], everything discarded+entered;
 * - empty `from`            -> retain = [] (nothing mounted to keep — e.g.
 *                              a directly loaded CSR shell), all of `to`
 *                              enters fresh;
 * - empty `to`              -> retain = [], everything from `from` goes.
 *
 * Throws LayoutStackError on malformed input (non-strings, blanks,
 * repeated IDs) — callers treat that as a protocol violation, not a guess.
 */
export function layoutStackTransition(
  from: readonly unknown[],
  to: readonly unknown[]
): LayoutStackTransition {
  const fromStack = normalizeLayoutStack(from);
  if (fromStack === null) {
    throw new LayoutStackError(`invalid outgoing layout stack: ${JSON.stringify(from)}`);
  }
  const toStack = normalizeLayoutStack(to);
  if (toStack === null) {
    throw new LayoutStackError(`invalid incoming layout stack: ${JSON.stringify(to)}`);
  }

  // Positional longest common prefix.
  let shared = 0;
  while (
    shared < fromStack.length &&
    shared < toStack.length &&
    fromStack[shared] === toStack[shared]
  ) {
    shared++;
  }

  return {
    retain: toStack.slice(0, shared),
    discard: fromStack.slice(shared).reverse(), // innermost first
    enter: toStack.slice(shared), // outermost first
  };
}

/**
 * Read the MOUNTED layout ancestry of a document: every element carrying
 * the layout marker, in document order (= outermost first, since wrappers
 * contain what follows them). Blank markers are ignored.
 *
 * This observes the DOM only — the one impure function here, and the only
 * source of truth for what can be retained. Note a directly loaded CSR
 * shell returns [] regardless of what any envelope declares: its boot
 * document contains no templ layout markup at all.
 *
 * The result is NOT validated for duplicates the way declare stacks are:
 * the DOM is what it is, and the transition planner compares it as-is.
 */
export function mountedLayoutStack(root: ParentNode): LayoutId[] {
  const stack: LayoutId[] = [];
  for (const el of Array.from(root.querySelectorAll(`[${LAYOUT_MARKER}]`))) {
    const id = el.getAttribute(LAYOUT_MARKER);
    if (id && id.trim() !== '') stack.push(id.trim());
  }
  return stack;
}
