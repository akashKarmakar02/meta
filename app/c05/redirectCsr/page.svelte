<script lang="ts">
  // C05 CSR redirect fixture. Its registered loader (LoadC05RedirectCSR)
  // always redirects to /dashboard, so in the real app this page never mounts —
  // the 302 fires before the envelope is built. The markup below only matters
  // for the no-loader test app (newApp(nil)), where the route renders normally
  // so the route-iterating contract tests still see a valid CSR shell.
</script>

<main data-go-page="c05-redirect-csr">
  <h1>C05 CSR redirect source</h1>
  <p>This page's loader redirects to /dashboard.</p>
</main>
