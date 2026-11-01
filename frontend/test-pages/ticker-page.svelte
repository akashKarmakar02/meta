<script lang="ts">
  // F09 teardown fixture: a page that owns two real subscriptions — an
  // interval and a document-level listener — both created on mount and
  // torn down in onDestroy. Tests assert the subscription stops firing
  // after unmount resolves.
  import { onDestroy } from 'svelte';

  let ticks = $state(0);
  let keypresses = $state(0);

  const interval = setInterval(() => {
    ticks++;
  }, 10);

  const onKeydown = () => {
    keypresses++;
  };
  document.addEventListener('keydown', onKeydown);

  onDestroy(() => {
    clearInterval(interval);
    document.removeEventListener('keydown', onKeydown);
  });
</script>

<main data-go-page="ticker">
  <p data-ticks>Ticks: {ticks}</p>
  <p data-keypresses>Keys: {keypresses}</p>
</main>
