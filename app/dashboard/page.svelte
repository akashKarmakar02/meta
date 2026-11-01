<!-- Dashboard page (E01): interactive counter, Svelte 5 runes.
     Mounted client-side into the #app shell element by Phase E mounting
     tasks; no router, no navigation, no fetching. -->
<script lang="ts">
  import SiteNav from '../components/SiteNav.svelte';
  // (app/components — shared with the other CSR pages)
  let count = $state(0);
</script>

<SiteNav />
<main data-go-page="dashboard">
  <h1>Dashboard</h1>
  <p>Count: {count}</p>
  <button type="button" onclick={() => count++}>Increment</button>
  <button type="button" onclick={() => count--}>Decrement</button>
</main>
