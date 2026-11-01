<!--
  Shared navigation for CSR pages. Direct loads of Svelte routes serve
  the bare boot shell (no templ layout), so each CSR page renders this
  nav itself; the F05 interceptor soft-navigates its links, and once a
  CSR page transitions to an SSR page the templ Nav in app/layout.templ
  takes over. Keep the href list in sync with app/nav.templ.
-->
<script lang="ts">
  // Static markup only; no props, no state.
  //
  // Direct CSR loads serve the bare boot shell with no templ layout, so
  // the page supplies its own nav. After a soft navigation from an SSR
  // page the persistent layout Nav is already in the document — render
  // nothing here to avoid a duplicate navbar (F12 keeps everything
  // outside the page outlet across soft navigations).
  let standalone = $state(
    !document.querySelector('[data-go-layout]'),
  );
</script>

{#if standalone}
<nav data-go-site-nav>
  <ul>
    <li><a href="/">Home</a></li>
    <li><a href="/about">About</a></li>
    <li><a href="/docs">Docs</a></li>
    <li><a href="/dashboard">Dashboard</a></li>
    <li><a href="/dashboard/settings">Settings</a></li>
    <li><a href="/products/new">New product</a></li>
  </ul>
</nav>
{/if}
