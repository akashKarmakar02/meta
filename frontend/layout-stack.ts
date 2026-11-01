// Layout stack identity and persistence planning (blueprint §4.3, §5.2
// steps 7-8; task G01).
//
// A LAYOUT STACK is a chain of layout IDs ordered OUTERMOST to INNERMOST
// — exactly the order the Go server emits in NavigationResponse.layouts,
// derived from the generated route manifest (routes_gen.go Route.Layouts),
// which mirrors the app tree. Each ID (e.g. "app/layout.templ",
// "app/dashboard/layout.svelte") names one layout FILE; the same ID always
// means the same layout, which is what makes stacks comparable.
//
// Two DIFFERENT kinds of stacks exist, and conflating them is the classic
// bug this module exists to prevent:
//
// - DECLARED ancestry: what route metadata says SHOULD wrap a page. It is
//   a wish, not a fact about the DOM. A directly-loaded CSR shell (the
//   bare boot document Fiber serves for /dashboard on a hard navigation)
//   declares ["app/layout.templ", ...] in its envelope but contains NO
//   root layout markup at all — the shell document mounts nothing but the
//   page itself.
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
 * - an ID may appear AT MOST ONCE in a stack: one ID names one layout
 *   instance, so a repeated ID claims the same wrapper twice — an
 *   ambiguous hierarchy the Phase 1 runtime refuses rather than guesses.
 *
 * Returns a new array of trimmed IDs, or null when the input is not a
 * valid stack.
 */
export function normalizeLayoutStack(
  ids: readonly unknown[]
): LayoutId[] | null {
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
