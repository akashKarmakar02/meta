<script lang="ts">
  // D06 fixture: an ACTUAL compiled Svelte page component rendering an
  // enhanced action form. Proves the delegated submit handler works across
  // the real Svelte mount/unmount lifecycle — not just dynamically
  // inserted DOM (D05's CSR test proved delegation; this proves the full
  // component lifecycle: fresh mount on re-entry, no duplicate listeners,
  // no stale feedback after unmount).
  let entries = $state<{ name: string; message: string }[]>([]);

  // WithFormCSRF runs on page GETs, so this browser already holds the
  // signed _meta_csrf cookie; the double-submit protocol needs the SAME
  // token in the hidden field. Client-rendered components read it from the
  // cookie (the middleware's context token only reaches templ renders).
  let csrfToken = $state('');
  $effect(() => {
    const match = document.cookie
      .split('; ')
      .find((c) => c.startsWith('_meta_csrf='));
    csrfToken = match ? decodeURIComponent(match.slice('_meta_csrf='.length)) : '';
  });

  function add(): void {
    const name = document.querySelector<HTMLInputElement>(
      'form[data-go-form="d06"] input[name="name"]',
    );
    const message = document.querySelector<HTMLInputElement>(
      'form[data-go-form="d06"] input[name="message"]',
    );
    if (!name || !message) return;
    entries.push({ name: name.value, message: message.value });
    name.value = '';
    message.value = '';
  }
</script>

<main data-go-page="d06-form">
  <h1>D06 Svelte form</h1>
  <!--
    data-go-meta-enhance opts into the delegated enhanced submission. The
    hidden _csrf field carries the double-submit token from the page cookie.
  -->
  <form method="post" action="/_meta/actions/guestbook.submit" data-go-form="d06" data-go-meta-enhance>
    <input type="hidden" name="_csrf" value={csrfToken} />
    <label for="d06-name">Name</label>
    <input id="d06-name" type="text" name="name" maxlength="80" />
    <label for="d06-message">Message</label>
    <input id="d06-message" type="text" name="message" maxlength="500" />
    <button type="submit">Submit locally</button>
  </form>
  <button type="button" onclick={add}>Track entry</button>
  <p data-d06-entry-count>{entries.length}</p>
  <ul data-d06-entries>
    {#each entries as entry (entry.name + entry.message)}
      <li>{entry.name}: {entry.message}</li>
    {/each}
  </ul>
</main>
