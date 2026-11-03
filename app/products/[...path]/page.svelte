<script lang="ts">
  // B03 CSR catch-all page. The client mounts by stable routeId (the declared
  // module, not the concrete URL), so the concrete path is a runtime detail.
  //
  // It is read from the page root's data-go-route attribute, NOT
  // window.location: the navigation pipeline commits the URL (history.pushState)
  // only AFTER the page is mounted, so window.location still holds the PREVIOUS
  // path at mount time. data-go-route is set to the destination path before the
  // component mounts (csr-mount / layout-mount), so it is always current.
  //
  // The pipeline remounts the page on every navigation — including same-routeId
  // catch-all navigations — so this always reflects the destination.
  const root = document.querySelector('[data-go-page-root]');
  const route = (root?.getAttribute('data-go-route') ?? window.location.pathname) || '';
  const path = route.split('?')[0]; // drop the query string
  const segments = path.split('/').filter(Boolean).slice(1); // drop "products"
</script>

<main data-go-page-products-path>
  <h1>Files</h1>
  <p data-catchall-path>{path}</p>
  <p data-catchall-segments>{segments.join(' > ')}</p>
  <nav data-products-path-nav>
    <a href="/products/reports/2026/summary.pdf">Summary</a>
    <a href="/products/reports/2026/summary.pdf/annotations">Annotations</a>
    <a href="/products/other/doc">Other doc</a>
    <a href="/about">About</a>
  </nav>
</main>
