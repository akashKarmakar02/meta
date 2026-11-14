<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { recoverSession, type RecoveryClerk } from '../browser/recovery';

  let { data, getClerk, onReady, onSignedOut, checkURL = '/auth/session' }: {
    data: { returnTo: string; signInURL: string; recoveryURL: string; retryRequired: boolean };
    getClerk: () => Promise<RecoveryClerk>;
    onReady: (destination: string) => void | Promise<void>;
    onSignedOut: (destination: string) => void | Promise<void>;
    checkURL?: string;
  } = $props();
  let state = $state<'checking' | 'failed'>(untrack(() => data.retryRequired ? 'failed' : 'checking'));
  let active = true;
  let controller: AbortController | undefined;

  async function check(): Promise<void> {
    controller?.abort();
    const attempt = new AbortController();
    controller = attempt;
    state = 'checking';
    const result = await recoverSession({ ...data, getClerk, checkURL, signal: attempt.signal });
    if (!active || attempt.signal.aborted || controller !== attempt) return;
    try {
      if (result.status === 'ready') await onReady(result.destination);
      else if (result.status === 'signed-out') await onSignedOut(result.signInURL);
      else state = 'failed';
    } catch { if (active) state = 'failed'; }
  }

  onMount(() => {
    active = true;
    if (!data.retryRequired) void check();
    return () => { active = false; controller?.abort(); };
  });
</script>

<main>
  <h1>Session check</h1>
  {#if state === 'checking'}
    <p role="status" aria-live="polite">Checking your session…</p>
  {:else}
    <p role="alert">We couldn’t confirm your session. Check your connection and try again.</p>
    <button type="button" onclick={check}>Try again</button>
    <a href={data.signInURL}>Continue to sign in</a>
  {/if}
  <noscript>JavaScript is needed to check your Clerk session.</noscript>
</main>
