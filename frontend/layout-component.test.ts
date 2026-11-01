// G02 tests: the Dashboard Svelte layout component (blueprint task G02:
// "Add Svelte dashboard/layout.svelte with sidebar and internal counter").
//
// Scope is the COMPONENT only — it renders a sidebar, owns an internal
// counter, and exposes the children-snippet contract that G03's
// persistent outlet and G04's runtime mounting will build on. Persistent
// mounting across navigations is deliberately NOT tested here; that is
// G04-G08 territory.

import { describe, it, expect, beforeEach } from 'vitest';
import { mount, unmount, createRawSnippet } from 'svelte';
import DashboardLayout from '../app/dashboard/layout.svelte';

/**
 * Mount the layout with optional snippet children, fresh each test.
 * children returns the HTML string for the child content; it is wrapped
 * in a raw snippet because {@render ...} requires a compiler-constructed
 * Snippet, and this test file is TypeScript, not a .svelte template.
 */
async function mountLayout(children?: () => string) {
  const target = document.createElement('div');
  document.body.append(target);
  const props: Record<string, unknown> = {};
  if (children) {
    props.children = createRawSnippet(() => ({
      render: () => `<main data-page-marker="child-page">${children()}</main>`,
    }));
  }
  const instance = mount(DashboardLayout, { target, props });
  return { target, instance };
}

beforeEach(() => {
  document.body.innerHTML = '';
});

describe('G02: DashboardLayout rendering', () => {
  it('renders the sidebar with navigation links', async () => {
    const { target } = await mountLayout();
    const sidebar = target.querySelector('[data-go-dashboard-sidebar]');
    expect(sidebar).toBeTruthy();
    const nav = target.querySelector('[data-go-dashboard-side-nav]');
    expect(nav).toBeTruthy();
    const links = [...target.querySelectorAll('a')].map((a) => a.getAttribute('href'));
    expect(links).toContain('/dashboard');
    expect(links).toContain('/dashboard/settings');
  });

  it('renders the internal counter starting at zero', async () => {
    const { target } = await mountLayout();
    const counter = target.querySelector('[data-go-layout-counter]');
    expect(counter?.textContent).toBe('Layout count: 0');
  });

  it('counter increments on button click (state changes)', async () => {
    const { target } = await mountLayout();
    const button = target.querySelector(
      '[data-go-layout-counter-button]'
    ) as HTMLButtonElement;
    expect(button).toBeTruthy();
    button.click();
    button.click();
    await new Promise((r) => setTimeout(r, 0));
    expect(
      target.querySelector('[data-go-layout-counter]')?.textContent
    ).toBe('Layout count: 2');
  });

  it('counter state is owned by the instance, not shared between mounts', async () => {
    const a = await mountLayout();
    const b = await mountLayout();
    (a.target.querySelector('[data-go-layout-counter-button]') as HTMLButtonElement).click();
    await new Promise((r) => setTimeout(r, 0));
    expect(
      a.target.querySelector('[data-go-layout-counter]')?.textContent
    ).toBe('Layout count: 1');
    // A separate instance is unaffected — no module-level shared state.
    expect(
      b.target.querySelector('[data-go-layout-counter]')?.textContent
    ).toBe('Layout count: 0');
  });
});

describe('G02: child-content contract', () => {
  it('renders supplied children inside the content region', async () => {
    const { target } = await mountLayout(() => 'child page body');
    const content = target.querySelector('[data-go-layout-content]');
    expect(content).toBeTruthy();
    // The snippet landed inside the content region...
    expect(
      content?.querySelector('[data-page-marker="child-page"]')?.textContent
    ).toBe('child page body');
    // ...not inside the sidebar.
    expect(
      target.querySelector('[data-go-dashboard-sidebar] [data-page-marker]')
    ).toBeNull();
  });

  it('keeps the sidebar stable while child content renders alongside it', async () => {
    // The contract G03+ builds on: arbitrary child content lives inside
    // [data-go-layout-content] while the layout (sidebar + counter) stays
    // mounted around it. At G02 this proves composition and separation;
    // swap-without-remount across navigations is G04-G06 machinery.
    const { target } = await mountLayout(() => 'settings page body');
    expect(target.querySelector('[data-go-layout-content]')?.textContent).toContain(
      'settings page body'
    );
    // Layout chrome present exactly once, surrounding the child.
    expect(target.querySelectorAll('[data-go-dashboard-sidebar]').length).toBe(1);
    expect(target.querySelectorAll('[data-go-layout-content]').length).toBe(1);
    expect(target.querySelector('[data-go-layout-counter]')?.textContent).toBe('Layout count: 0');
  });

  it('renders an empty outlet when no children are given (G03)', async () => {
    const { target } = await mountLayout();
    // G03: the content region contains exactly one, empty child outlet.
    expect(target.querySelector('[data-go-layout-content]')).toBeTruthy();
    const outlets = target.querySelectorAll('[data-go-layout-outlet]');
    expect(outlets.length).toBe(1);
    expect(outlets[0].children.length).toBe(0);
  });
});

describe('G02: lifecycle', () => {
  it('unmounts cleanly via Svelte 5 unmount', async () => {
    const { instance, target } = await mountLayout();
    await unmount(instance);
    expect(target.querySelector('[data-go-dashboard-sidebar]')).toBeNull();
    expect(document.body.contains(target)).toBe(true); // target itself is caller-owned
  });
});
