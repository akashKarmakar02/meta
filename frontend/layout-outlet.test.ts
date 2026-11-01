// G03 tests: the explicit content outlet inside persistent Svelte layouts
// (blueprint task G03: "sub-page can mount without replacing layout DOM").
//
// Scope is the OUTLET CONTRACT only — structure, ownership boundary, and
// layout-instance survival across child-outlet changes. Runtime mounting
// (G04), child-only navigation (G05), and persistence across navigations
// (G06-G08) are not tested here.

import { describe, it, expect, beforeEach } from 'vitest';
import { mount, unmount } from 'svelte';
import DashboardLayout from '../app/dashboard/layout.svelte';
import {
  locateLayoutOutlets,
  isInsideLayoutOutlet,
  requireEmptyOutlet,
  LayoutOutletError,
  LayoutOutletSelector,
} from './layout-outlet';

/** Mount a fresh Dashboard layout instance; caller unmounts via cleanup(). */
function mountLayout() {
  const target = document.createElement('div');
  document.body.append(target);
  const instance = mount(DashboardLayout, { target });
  return { target, instance };
}

beforeEach(() => {
  document.body.innerHTML = '';
});

describe('G03: outlet structure', () => {
  it('exactly one designated child outlet exists', async () => {
    const { target } = mountLayout();
    expect(target.querySelectorAll(LayoutOutletSelector).length).toBe(1);
  });

  it('the child outlet is inside the Dashboard layout', async () => {
    const { target } = mountLayout();
    const outlet = target.querySelector<HTMLElement>(LayoutOutletSelector);
    expect(outlet).toBeTruthy();
    // Nested within the layout's own DOM, not a sibling of it.
    expect(target.contains(outlet!)).toBe(true);
    expect(outlet!.closest('[data-go-layout-shell]')).toBeTruthy();
  });

  it('the sidebar and counter are OUTSIDE the child outlet', async () => {
    const { target } = mountLayout();
    const outlet = target.querySelector<HTMLElement>(LayoutOutletSelector)!;
    expect(outlet.contains(target.querySelector('[data-go-dashboard-sidebar]')!)).toBe(false);
    expect(outlet.contains(target.querySelector('[data-go-layout-counter]')!)).toBe(false);
    // And they are present exactly once at the layout level.
    expect(target.querySelectorAll('[data-go-dashboard-sidebar]').length).toBe(1);
    expect(target.querySelectorAll('[data-go-layout-counter]').length).toBe(1);
  });

  it('child content lands in the outlet when composed via snippet', async () => {
    const { target, instance } = mountLayout();
    // Compose child content imperatively into the outlet interior —
    // the navigation-runtime side of the ownership boundary — to prove
    // the designated location works for foreign (runtime-owned) DOM.
    const outlet = target.querySelector<HTMLElement>(LayoutOutletSelector)!;
    const pageRoot = document.createElement('main');
    pageRoot.setAttribute('data-go-page-root', 'test-page');
    pageRoot.textContent = 'page body';
    outlet.append(pageRoot);

    expect(
      target.querySelector('[data-go-page-root="test-page"]')?.closest(LayoutOutletSelector)
    ).toBe(outlet);
    // Cleanup the runtime-owned node before layout unmount.
    pageRoot.remove();
    await unmount(instance);
  });
});

describe('G03: layout-instance survival across outlet changes', () => {
  it('clearing the outlet does not destroy the parent layout instance', async () => {
    const { target, instance } = mountLayout();
    const outlet = target.querySelector<HTMLElement>(LayoutOutletSelector)!;

    // Runtime mounts a child, then replaces it (swap), then clears.
    const first = document.createElement('main');
    first.textContent = 'first page';
    outlet.append(first);
    const second = document.createElement('main');
    second.textContent = 'second page';
    first.replaceWith(second); // swap children — outlet element itself stays
    first.remove();
    second.remove();           // clear outlet — layout must remain mounted

    expect(target.querySelector('[data-go-dashboard-sidebar]')).toBeTruthy();
    expect(target.querySelector('[data-go-layout-counter]')?.textContent).toBe('Layout count: 0');
    // The SAME outlet element persisted through the swaps.
    expect(target.querySelector<HTMLElement>(LayoutOutletSelector)).toBe(outlet);
    await unmount(instance);
  });

  it('sidebar and counter keep state during child-only changes', async () => {
    const { target, instance } = mountLayout();
    const outlet = target.querySelector<HTMLElement>(LayoutOutletSelector)!;
    const flush = () => new Promise((r) => setTimeout(r, 0));

    // Counter interacts before, during, and after child swaps.
    const btn = target.querySelector<HTMLButtonElement>('[data-go-layout-counter-button]')!;
    btn.click(); // count = 1
    await flush();

    const page = document.createElement('main');
    page.textContent = 'a page';
    outlet.append(page);
    page.replaceWith(document.createElement('main')); // child-only change
    btn.click(); // count = 2 — interactive while children changed
    await flush();

    expect(target.querySelector('[data-go-layout-counter]')?.textContent).toBe('Layout count: 2');
    // The component is still a live Svelte instance: reactivity still flows.
    btn.click();
    await flush();
    expect(target.querySelector('[data-go-layout-counter]')?.textContent).toBe('Layout count: 3');
    outlet.firstElementChild?.remove();
    await unmount(instance);
  });

  it('unmounting a child page mounted in the outlet leaves the layout alive', async () => {
    // The persistent-parent-must-survive rule, expressed with a real
    // Svelte child mounted into the runtime-owned outlet interior.
    const { target, instance } = mountLayout();
    const outlet = target.querySelector<HTMLElement>(LayoutOutletSelector)!;
    const pageTarget = document.createElement('div');
    outlet.append(pageTarget); // runtime creates the mount root (G04 role)
    const child = mount(DashboardLayout, { target: pageTarget }); // any component
    await unmount(child);
    // Child component gone; layout untouched. The runtime-owned mount
    // ROOT element remains — removing it is the runtime's job, not
    // Svelte's and not the child's.
    expect(target.querySelector('[data-go-dashboard-sidebar]')).toBeTruthy();
    expect(outlet.isConnected).toBe(true);
    expect(outlet.contains(pageTarget)).toBe(true);
    pageTarget.remove(); // runtime honors the ownership boundary
    await unmount(instance);
  });
});

describe('G03: requireEmptyOutlet contract enforcement', () => {
  it('returns the single empty outlet of a fresh layout', async () => {
    const { target, instance } = mountLayout();
    const shell = target.querySelector<HTMLElement>('[data-go-layout-shell]')!;
    const outlet = requireEmptyOutlet(shell);
    expect(outlet.getAttribute('data-go-layout-outlet') !== null).toBe(true);
    await unmount(instance);
  });

  it('throws LayoutOutletError when no outlet exists', () => {
    const bare = document.createElement('div');
    document.body.append(bare);
    expect(() => requireEmptyOutlet(bare)).toThrow(LayoutOutletError);
  });

  it('throws LayoutOutletError when more than one outlet exists', () => {
    const el = document.createElement('div');
    el.innerHTML = '<div data-go-layout-outlet></div><div data-go-layout-outlet></div>';
    document.body.append(el);
    expect(() => requireEmptyOutlet(el)).toThrow(/exactly one/);
  });

  it('throws when the outlet is occupied — snippet and runtime mounts are exclusive', async () => {
    const { target, instance } = mountLayout();
    const shell = target.querySelector<HTMLElement>('[data-go-layout-shell]')!;
    const outlet = shell.querySelector<HTMLElement>(LayoutOutletSelector)!;
    outlet.append(document.createElement('main'));
    expect(() => requireEmptyOutlet(shell)).toThrow(/not empty/);
    outlet.firstChild?.remove();
    await unmount(instance);
  });
});

describe('G03: locateLayoutOutlets / isInsideLayoutOutlet', () => {
  it('locates outlets outermost-first across nested layouts', async () => {
    const { target, instance } = mountLayout();
    // Nest a second layout inside the first one's outlet (composition).
    const outerOutlet = target.querySelector<HTMLElement>(LayoutOutletSelector)!;
    const innerHost = document.createElement('section');
    outerOutlet.append(innerHost);
    const inner = mount(DashboardLayout, { target: innerHost });

    const outlets = locateLayoutOutlets(document);
    expect(outlets.length).toBe(2);
    expect(outlets[0].contains(outlets[1])).toBe(true); // outermost first

    await unmount(inner);
    innerHost.remove();
    await unmount(instance);
  });

  it('isInsideLayoutOutlet distinguishes runtime-owned from Svelte-owned nodes', async () => {
    const { target, instance } = mountLayout();
    const outlet = target.querySelector<HTMLElement>(LayoutOutletSelector)!;
    const pageNode = document.createElement('main');
    outlet.append(pageNode);

    expect(isInsideLayoutOutlet(pageNode)).toBe(true);
    expect(isInsideLayoutOutlet(outlet)).toBe(true); // the outlet itself
    expect(isInsideLayoutOutlet(target.querySelector('[data-go-dashboard-sidebar]')!)).toBe(false);
    expect(isInsideLayoutOutlet(null)).toBe(false);
    pageNode.remove();
    await unmount(instance);
  });
});

describe('G03: layout unmount cleanup contract', () => {
  it('unmounting the layout removes the outlet element entirely', async () => {
    const { target, instance } = mountLayout();
    const shell = target.querySelector('[data-go-layout-shell]')!;
    await unmount(instance);
    expect(document.querySelector(LayoutOutletSelector)).toBeNull();
    expect(document.contains(shell)).toBe(false);
  });

  it('an unmounted runtime-owned child left behind does not break the next layout mount', async () => {
    // Ownership violation guard: if the runtime forgot to remove its node,
    // the next mount's requireEmptyOutlet catches it loudly instead of
    // silently double-mounting (no duplicate mount roots).
    const { target, instance } = mountLayout();
    const outlet = target.querySelector<HTMLElement>(LayoutOutletSelector)!;
    outlet.append(document.createElement('main'));
    await unmount(instance); // layout gone, stray node went with it
    expect(document.querySelectorAll(LayoutOutletSelector).length).toBe(0);

    const second = mountLayout();
    expect(second.target.querySelectorAll(LayoutOutletSelector).length).toBe(1);
    // Fresh layout, fresh outlet: contract holds.
    let ok = false;
    try {
      requireEmptyOutlet(second.target);
      ok = true;
    } finally {
      await unmount(second.instance);
    }
    expect(ok).toBe(true);
  });
});
