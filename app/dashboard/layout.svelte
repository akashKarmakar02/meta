<script lang="ts">
  // G02: the Dashboard subtree's Svelte layout — a sidebar with an
  // internal counter plus the content area where the current page's
  // output is rendered.
  //
  // G03: the content area contains an EXPLICIT child outlet element,
  // [data-go-layout-outlet]. That element is the stable mount point for
  // sub-pages: a child page mounts INSIDE it without replacing any layout
  // DOM, so this component instance (sidebar + counter state) survives
  // child-only navigation.
  //
  // Ownership boundary (G03):
  // - SVELTE owns the outlet element itself and everything outside it
  //   (sidebar, counter, content wrapper). Unmounting this component
  //   removes the outlet element with the whole layout.
  // - The NAVIGATION RUNTIME owns the outlet's INTERIOR: it creates the
  //   page mount root inside the outlet (G04+) and must unmount that
  //   instance before the layout unmounts. This component never touches
  //   the outlet's children.
  // - The optional children snippet renders inside the outlet for direct
  //   component composition (tests, non-runtime embedding). Snippet use
  //   and runtime mounts are mutually exclusive — never both at once.
  //
  // Counter state is owned by this component instance ($state rune, not a
  // module-level global): when the layout persists across child-only
  // swaps, the count survives because it lives here, not in a page or
  // module scope.
  //
  // G04: the shell carries this layout's stable ID in data-go-layout —
  // the same marker templ layouts stamp. It becomes TRUTHFUL only through
  // the runtime: mountPersistentLayout mounts this component inside a
  // [data-go-layout-shell-host] target and validates this marker exists
  // exactly once before treating the layout as mounted (G01's
  // mounted-vs-declared distinction; env.layouts alone proves nothing).
  import type { Snippet } from 'svelte';

  let { children }: { children?: Snippet } = $props();

  let count = $state(0);
</script>

<div data-go-layout-shell data-go-layout="app/dashboard/layout.svelte" class="dashboard-layout">
  <aside data-go-dashboard-sidebar>
    <h2>Dashboard</h2>
    <nav data-go-dashboard-side-nav>
      <ul>
        <li><a href="/dashboard">Overview</a></li>
        <li><a href="/dashboard/settings">Settings</a></li>
        <li><a href="/about">About</a></li>
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
    <!-- Accessibility: the outlet is the layout's main region; pages land
         inside it with their own landmark elements. -->
    <div data-go-layout-outlet role="presentation">
      {@render children?.()}
    </div>
  </div>
</div>
