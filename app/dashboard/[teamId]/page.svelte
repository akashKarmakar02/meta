<script lang="ts">
  // C04 demonstration CSR page. The `data` prop is the JSON-serialized result
  // of the Go loader (LoadDashboard), delivered by the framework before mount —
  // it is NOT derived from window.location. Greeting ("Loaded by Go") cannot be
  // produced from the URL alone, so displaying it proves the value came from
  // the server loader.
  type DashboardData = {
    teamId: string;
    title: string;
    greeting: string;
  };

  let { data }: { data: DashboardData | undefined } = $props();
</script>

<main data-go-page="dashboard-team">
  {#if data}
    <h1>{data.title}</h1>
    <p data-go-greeting>{data.greeting}: Team {data.teamId}</p>
  {:else}
    <p data-go-no-data>No loader data for this route.</p>
  {/if}
</main>
