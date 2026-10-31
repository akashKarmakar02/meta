<script lang="ts">
  let saved = $state(false);

  function save(): void {
    saved = true;
  }
</script>

<main data-go-page="dashboard/settings">
  <h1>Settings</h1>
  <p>Notification preferences, appearance, and account settings live here.</p>
  {#if saved}
    <p role="status">Saved.</p>
  {/if}
  <button type="button" onclick={save}>Save settings</button>
</main>
