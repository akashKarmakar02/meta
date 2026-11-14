<script lang="ts">
  import RecoveryPage from '../../../integrations/clerk/examples/RecoveryPage.svelte';
  import type { RecoveryClerk } from '../../../integrations/clerk/browser/recovery';
  import { navigateTo } from '../../../frontend/navigation-pipeline';
  let { data } = $props();
  // Simulates the supported SDK interface only. No live Clerk instance is used.
  let singleton: Promise<RecoveryClerk> | undefined;
  function getClerk(): Promise<RecoveryClerk> {
    const existing = (window as unknown as { Clerk?: RecoveryClerk }).Clerk;
    if (existing?.loaded) return Promise.resolve(existing);
    return singleton ??= (async () => {
      const state = await (await fetch('/fixture/client')).json();
      return { loaded: true, session: state.signedIn ? { async getToken() {
        const response = await fetch('/fixture/refresh');
        if (!response.ok) throw new Error('simulated refresh failure');
        return 'fixture-token-not-used-as-identity';
      } } : null };
    })();
  }
</script>

<RecoveryPage {data} {getClerk} onReady={navigateTo} onSignedOut={url => window.location.replace(url)} />
