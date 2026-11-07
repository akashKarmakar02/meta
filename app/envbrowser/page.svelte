<script lang="ts">
  const message = import.meta.env.VITE_PUBLIC_MESSAGE || 'Set VITE_PUBLIC_MESSAGE in .env and restart meta dev.';
</script>

<main>
  <h1>Browser environment example</h1>
  <p>{message}</p>
  <a href="/envexample">See the server environment example</a>
</main>
