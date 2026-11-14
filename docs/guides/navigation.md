# Navigation loading

Meta exposes soft-navigation state in `frontend/navigation-state.ts`. It works
with Svelte and plain TypeScript, including eligible link clicks, `navigateTo`,
and Back/Forward. No application link interception is needed.

```ts
import { getNavigationState, subscribeNavigation } from './frontend/navigation-state';
import { navigateTo } from './frontend/navigation-pipeline';

console.log(getNavigationState());
const unsubscribe = subscribeNavigation((state) => {
  // Called immediately with the snapshot, then synchronously on each change.
  console.log(state.status, state.attempt?.id, state.attempt?.url);
  if (state.outcome?.type === 'failed') console.error(state.outcome.error);
});

const controller = new AbortController();
await navigateTo('/dashboard', { signal: controller.signal });
// controller.abort() cancels an in-flight attempt; it cannot undo a commit.
unsubscribe(); // also unsubscribe when your application UI is destroyed
```

`getNavigationState()` returns an immutable snapshot. `subscribeNavigation(fn)`
returns an unsubscribe function. Subscriber exceptions are contained. The
snapshot has these fields:

| Field | Meaning |
| --- | --- |
| `status` | `idle` or `pending` |
| `attempt` | `null` initially; otherwise `{ id, url, source }` |
| `outcome` | `null` initially/while pending; terminal result while idle |

IDs increase for every attempt, including rejected guards. URLs are absolute
requested destinations; redirects can commit a different URL. `source` is
`link`, `programmatic`, or `history`. The idle snapshot retains the last
terminal result until the next state change; it is not a log. Each attempt
publishes at most one terminal result:

| `outcome.type` | Meaning |
| --- | --- |
| `completed` | Destination mounted/rendered and URL, title, metadata, scroll, and focus committed |
| `canceled` | `reason`: `guard`, `aborted`, `superseded`, or `stopped` |
| `failed` | `error` contains a guard, fetch, protocol, import, or mount failure; Meta initiates its existing full-document fallback |
| `fallback` | The destination requires a full document because the runtime cannot safely reconcile the mounted layout |

Failures and required full-document transitions clear pending **before** handing
off to the browser. They never report soft-navigation completion. Superseded
work cannot clear a newer attempt's pending state or trigger a late fallback.
`navigateTo()` retains its `Promise<void>` interface; observe state for outcomes.

## Optional progress indicator

The default entry starts a small indeterminate top bar after 150ms of pending.
Fast navigations show no bar. It uses a polite accessible “Loading page” status,
respects reduced motion, and sits directly under `document.body`, outside page
and layout outlets. Completion and cancellation remove it immediately. There
are no percentages or animation delays on navigation.

Disable the built-in UI by changing the existing startup call in
`frontend/entry.ts`:

```ts
startSoftNavigation({ progress: false });
```

Configure that call before startup; starting an already active runtime is a
no-op. `stopSoftNavigation()` cancels pending work and cleans up the indicator,
timer, and subscription. For manual installation, `installNavigationProgress()`
from `frontend/navigation-progress.ts` returns the same cleanup function; install
it only once and disable the automatic instance first.

## Svelte example

Save this as `frontend/NavigationStatus.svelte`, and render `<NavigationStatus />`
in a persistent Svelte layout. Disable the built-in indicator if this replaces
it. This example uses Svelte 5 state and cleans up its subscription on unmount.
It deliberately shows pending immediately; the default bar provides the 150ms
delay when that behavior is desired.

```svelte
<script lang="ts">
  import { onMount } from 'svelte';
  import { getNavigationState, subscribeNavigation } from './navigation-state';

  let navigation = $state(getNavigationState());
  onMount(() => subscribeNavigation((state) => { navigation = state; }));
</script>

<span role="status" aria-live="polite">
  {navigation.status === 'pending' ? 'Loading page…' : ''}
</span>
```

For example, a layout at `app/dashboard/layout.svelte` can import it with
`import NavigationStatus from '../../frontend/NavigationStatus.svelte'`.
Subscriptions inside a page end when that page unmounts; use a persistent
layout or an application-owned element outside the page outlet for continuous
observation. Custom indicators should avoid moving focus or replacing the
layout outlet element.

## Guards and lifecycle

`beforeNavigate` in `frontend/navigation-guards.ts` registers a guard for links,
programmatic navigation, and history traversal, and returns an unregister
function. Guards run in registration order before pending and fetching. Return
`false` to cancel, or return/resolve `true` or `undefined` to approve. Throwing
or rejecting fails the attempt and uses the normal document fallback. Async
guards receive the attempt's abort signal for cooperative cancellation:

```ts
import { beforeNavigate } from './frontend/navigation-guards';

const removeGuard = beforeNavigate(({ signal }) => {
  if (signal.aborted) return false;
  return !hasUnsavedChanges() || window.confirm('Leave without saving?');
});
// Call removeGuard() when the owning form/component is destroyed.
```

Rejected guards emit an idle cancellation without a pending state. Back/Forward
cancellation restores the prior Meta-managed history entry without adding an
entry. Guards are browser UI hooks, not authentication or authorization checks.

Pending covers the navigation response, destination JavaScript and its Vite
CSS dependencies, required client layouts, and destination mounting/Svelte's
DOM flush. Downloads finish before outgoing teardown; the old interface stays
available while waiting. Navigation preserves existing layout ownership,
focus, scroll restoration, and native link exclusions. Fragment-only links,
programmatic fragment changes, and same-document history traversal do not
start loading. Browser-native links (downloads, external destinations, modified
clicks, etc.) do not publish soft-navigation state.

Completion does **not** wait for application-owned asynchronous `onMount`
fetches, images, fonts, or other background work. Applications own those
loading states. Cancellation prevents future commits; it does not roll back
DOM teardown that has already started. A mount failure after teardown uses the
document fallback.

This feature does not eliminate authentication redirects, change server route
policies, or provide a loading shell for initial document loads. The optional
bar only observes soft navigation after the client runtime starts. No
`loading.svelte`/`loading.templ`, prefetching, or response cache is introduced.

## Existing projects

For a version-pinned upgrade, follow the
[beta.7 migration guide](releases.md#to-v010-beta7).

The browser runtime is copied by `meta init`; updating only the Go module or
CLI will not update it. Merge the new `navigation-state.ts`,
`navigation-guards.ts`, and `navigation-progress.ts` plus the updated
`navigation-pipeline.ts`, `navigation-history.ts`, `csr-mount.ts`, and
`layout-mount.ts` from the framework runtime. Preserve application edits and
configure the existing entry startup call as needed. Run `meta generate` and
rebuild client assets. New projects receive these files through the embedded
runtime sources automatically; no new dependency or navigation protocol field
is required.
