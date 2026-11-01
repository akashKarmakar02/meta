<script lang="ts">
  // G02: the Dashboard subtree's Svelte layout — a sidebar with an
  // internal counter plus the content area where the current page's
  // output is rendered.
  //
  // Child-content contract: the layout takes one optional `children`
  // snippet prop (Svelte 5 runes idiom) and renders it inside
  // [data-go-layout-content]. Pages (and, from G03 on, the persistent
  // outlet machinery) hand their content in through that snippet — the
  // layout never knows which page is inside it, so it can eventually host
  // different child pages without being remounted.
  //
  // Counter state is owned by this component instance ($state rune, not a
  // module-level global): when G04-G06 make the layout persist across
  // child-only swaps, the count survives because it lives here, not in a
  // page or module scope.
  //
  // NOTE: this wrapper deliberately does NOT carry the data-go-layout
  // marker. Those markers mean "a layout INSTANCE the navigation runtime
  // mounted" (G01's mounted-vs-declared distinction); until G04 owns
  // mounting, the layout renders per-page as ordinary composition and
  // must not be mistaken for a mounted persistent layout instance.
  import type { Snippet } from 'svelte';

  let { children }: { children?: Snippet } = $props();

  let count = $state(0);
</script>

<div data-go-layout-shell class="dashboard-layout">
  <aside data-go-dashboard-sidebar>
    <h2>Dashboard</h2>
    <nav data-go-dashboard-side-nav>
      <ul>
        <li><a href="/dashboard">Overview</a></li>
        <li><a href="/dashboard/settings">Settings</a></li>
      </ul>
    </nav>
    <p data-go-layout-counter>Layout count: {count}</p>
    <button
      type="button"
      data-go-layout-counter-button
      onclick={() => count++}
    >
      Increment layout counter
    </button>
  </aside>
  <div data-go-layout-content class="dashboard-content">
    {@render children?.()}
  </div>
</div>
