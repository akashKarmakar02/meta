<script lang="ts">
  // C06 three-state fixture: its registered loader (LoadC05NullData) returns
  // a JSON null. The page renders a marker distinguishing an explicit null
  // payload from both an absent payload (no loader) and a value payload —
  // the three states a CSR page can see in its `data` prop.
  type NullData = { marker: string } | null;

  let { data }: { data: NullData | undefined } = $props();
</script>

<main data-go-page="c05-null-data">
  {#if data === undefined}
    <p data-go-null-state>absent: undefined</p>
  {:else if data === null}
    <p data-go-null-state>explicit null</p>
  {:else}
    <p data-go-null-state>value: {data.marker}</p>
  {/if}
</main>
