<script lang="ts">
  let { data }: { data: { id: string; tab: string; refreshed: string } } = $props();
</script>
<main data-cache-demo="csr">
  <h1>Cached team</h1>
  <p data-cache-id>{data.id}</p>
  <p data-cache-tab>{data.tab}</p>
  <p data-cache-refreshed>{data.refreshed}</p>
</main>
