// F09 tests: CSR unmount on the outgoing page tears down its
// subscriptions, and mount/unmount instrumentation is stable.
//
// "Stable" (blueprint acceptance): every mount emits exactly one
// 'mounted' event and every unmount exactly one 'unmounted'; the live
// instance count returns to zero; events are ordered; instrumentation
// survives thrown listeners; repeated soft navigations stay balanced.
import { describe, it, expect, beforeEach } from 'vitest';
import {
  mountComponent,
  onLifecycleEvent,
  liveInstanceCount,
  type LifecycleEvent,
} from './svelte-mounter';
import Dashboard from '../app/dashboard/page.svelte';
import Settings from '../app/dashboard/settings/page.svelte';
import Ticker from './test-pages/ticker-page.svelte';

describe('F09: subscription teardown on outgoing pages', () => {
  let target: HTMLElement;

  beforeEach(() => {
    target = document.createElement('div');
    document.body.appendChild(target);
  });

  it('stops interval and listener subscriptions after unmount', async () => {
    const handle = mountComponent(Ticker, { target });
    expect(target.textContent).toContain('Ticks: 0');

    // The subscription fires while mounted.
    await new Promise((r) => setTimeout(r, 35));
    expect(target.querySelector('[data-ticks]')!.textContent).not.toBe('Ticks: 0');

    // Capture the node BEFORE unmount: Svelte 5 removes the component's
    // DOM from the target during unmount, so we read the detached node.
    const ticksNode = target.querySelector('[data-ticks]')!;
    await handle.unmount();

    // After unmount resolves, the interval is cleared: the detached node
    // shows a frozen count no matter how long we wait.
    const frozen = ticksNode.textContent;
    await new Promise((r) => setTimeout(r, 40));
    expect(ticksNode.textContent).toBe(frozen);
  });

  it('removes document-level listeners on unmount', async () => {
    const handle = mountComponent(Ticker, { target });
    const keydown = () =>
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'a' }));
    keydown();
    keydown();
    // Let the $state update flush to the DOM.
    await new Promise((r) => setTimeout(r, 0));
    const keysNode = target.querySelector('[data-keypresses]')!;
    expect(keysNode.textContent).toBe('Keys: 2');

    await handle.unmount();
    // Listener removed with the instance: further events cannot reach
    // the detached node.
    keydown();
    keydown();
    await new Promise((r) => setTimeout(r, 10));
    expect(keysNode.textContent).toBe('Keys: 2');
  });
});

describe('F09: mount/unmount instrumentation stability', () => {
  let target: HTMLElement;
  let events: LifecycleEvent[];

  beforeEach(() => {
    target = document.createElement('div');
    document.body.appendChild(target);
    events = [];
  });

  it('emits exactly one mounted and one unmounted per lifecycle', async () => {
    const stop = onLifecycleEvent((e) => events.push(e));
    const handle = mountComponent(Dashboard, { target });
    await handle.unmount();
    stop();

    expect(events.map((e) => e.phase)).toEqual(['mounted', 'unmounted']);
    expect(events[0].target).toBe(target);
    expect(events[1].target).toBe(target);
  });

  it('tracks the live instance count across many transitions', async () => {
    const before = liveInstanceCount();
    let handle = mountComponent(Dashboard, { target });
    expect(liveInstanceCount()).toBe(before + 1);

    for (let i = 0; i < 5; i++) {
      // Soft-navigation shape: unmount outgoing, then mount incoming
      // into a fresh target (the E04 mounter rejects occupied targets).
      const outgoing = handle;
      handle = mountComponent(i % 2 === 0 ? Settings : Dashboard, {
        target: document.createElement('div'),
      });
      await outgoing.unmount();
    }
    await handle.unmount();
    expect(liveInstanceCount()).toBe(before);
  });

  it('returns to zero after every mounted instance is unmounted', async () => {
    const start = liveInstanceCount();
    const handles = [
      mountComponent(Dashboard, { target: document.createElement('div') }),
      mountComponent(Settings, { target: document.createElement('div') }),
      mountComponent(Ticker, { target: document.createElement('div') }),
    ];
    expect(liveInstanceCount()).toBe(start + 3);

    await Promise.all(handles.reverse().map((h) => h.unmount()));
    expect(liveInstanceCount()).toBe(start);
  });

  it('contains listener failures without breaking transitions', async () => {
    const stop1 = onLifecycleEvent(() => {
      throw new Error('listener bug');
    });
    let secondSawMounted = false;
    const stop2 = onLifecycleEvent((e) => {
      if (e.phase === 'mounted') secondSawMounted = true;
    });

    const handle = mountComponent(Dashboard, { target });
    await handle.unmount();

    stop1();
    stop2();
    expect(secondSawMounted).toBe(true);
    expect(handle.mounted).toBe(false);
    expect(target.children.length).toBe(0);
  });

  it('stays balanced across repeated soft navigation cycles', async () => {
    const start = liveInstanceCount();
    let handle = mountComponent(Dashboard, { target });
    for (let i = 0; i < 4; i++) {
      // Soft-navigation shape: unmount outgoing, then mount incoming —
      // exactly what mountCsrPage does in F08's transition path.
      const outgoing = handle;
      handle = mountComponent(i % 2 === 0 ? Settings : Dashboard, {
        target: document.createElement('div'),
      });
      await outgoing.unmount();
    }
    await handle.unmount();
    expect(liveInstanceCount()).toBe(start);
  });

  it('unsubscribe stops delivery', async () => {
    let received = 0;
    const stop = onLifecycleEvent(() => {
      received++;
    });
    const a = mountComponent(Dashboard, { target: document.createElement('div') });
    await a.unmount();
    stop();

    const b = mountComponent(Dashboard, { target: document.createElement('div') });
    await b.unmount();
    expect(received).toBe(2);
  });
});
describe('F09: transition ordering (outgoing before incoming)', () => {
  it('records phases in mounted -> unmounted order across a soft navigation', async () => {
    const events: string[] = [];
    const stop = onLifecycleEvent((e) => events.push(e.phase));

    const outgoing = mountComponent(Dashboard, {
      target: document.createElement('div'),
    });
    const incoming = mountComponent(Settings, {
      target: document.createElement('div'),
    });
    await outgoing.unmount();
    await incoming.unmount();

    stop();
    expect(events).toEqual([
      'mounted',   // outgoing
      'mounted',   // incoming
      'unmounted', // outgoing torn down first
      'unmounted', // incoming later
    ]);
  });
});
