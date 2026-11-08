<script lang="ts">
  import { getAllContexts } from 'svelte';
  import { registerOutletContext } from './layout-context';

  const context = getAllContexts();

  function capture(node: HTMLElement) {
    const unregister = registerOutletContext(node, context);
    return { destroy: unregister };
  }
</script>

<div data-go-layout-outlet use:capture></div>
