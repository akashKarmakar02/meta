// E04 lifecycle test: mount the Dashboard counter into a real DOM
// element, verify interactivity, unmount, and verify cleanup. Runs in a
// happy-dom environment (Node at test time only — production runs the Go
// binary plus prebuilt client assets, no Node).
import { describe, it, expect, beforeEach } from 'vitest';
import { mountComponent } from './svelte-mounter';
import Dashboard from '../app/dashboard/page.svelte';

describe('mountComponent lifecycle', () => {
  let target: HTMLElement;

  beforeEach(() => {
    target = document.createElement('div');
    target.id = 'app';
    document.body.appendChild(target);
  });

  it('mounts the Dashboard and renders the counter', () => {
    const handle = mountComponent(Dashboard, { target });
    expect(handle.target).toBe(target);
    expect(handle.mounted).toBe(true);
    expect(target.querySelector('[data-go-page="dashboard"]')).toBeTruthy();
    expect(target.textContent).toContain('Count: 0');
  });

  it('renders an interactive counter that responds to clicks', async () => {
    mountComponent(Dashboard, { target });
    expect(target.textContent).toContain('Count: 0');

    (target.querySelector('button') as HTMLButtonElement).click();
    await Promise.resolve();
    expect(target.textContent).toContain('Count: 1');

    (target.querySelector('button') as HTMLButtonElement).click();
    await Promise.resolve();
    expect(target.textContent).toContain('Count: 2');
  });

  it('unmounts and removes rendered content from the DOM', async () => {
    const handle = mountComponent(Dashboard, { target });
    expect(target.children.length).toBeGreaterThan(0);

    await handle.unmount();
    expect(handle.mounted).toBe(false);
    // Svelte removes the component's DOM nodes on unmount.
    expect(target.children.length).toBe(0);
    expect(target.textContent).toBe('');
  });

  it('unmount is idempotent — a second call after completion is a no-op', async () => {
    const handle = mountComponent(Dashboard, { target });
    await handle.unmount();
    await handle.unmount();
    expect(target.children.length).toBe(0);
  });

  it('rejects mounting when no target is supplied', () => {
    expect(() => mountComponent(Dashboard, {} as never)).toThrow(/target/);
  });

  it('rejects mounting into an occupied target (no duplicate instances)', () => {
    mountComponent(Dashboard, { target });
    expect(() => mountComponent(Dashboard, { target })).toThrow(/already has a mounted/);
    // The rejected second mount left exactly one instance behind.
    expect(target.querySelectorAll('[data-go-page="dashboard"]').length).toBe(1);
  });

  it('frees the target for remounting after unmount completes', async () => {
    const first = mountComponent(Dashboard, { target });
    await first.unmount();

    const second = mountComponent(Dashboard, { target });
    expect(second.mounted).toBe(true);
    expect(target.querySelectorAll('[data-go-page="dashboard"]').length).toBe(1);
  });

  it('keeps instances isolated: mounting one target never affects another', () => {
    const other = document.createElement('div');
    document.body.appendChild(other);

    mountComponent(Dashboard, { target });
    mountComponent(Dashboard, { target: other });

    expect(target.querySelectorAll('[data-go-page="dashboard"]').length).toBe(1);
    expect(other.querySelectorAll('[data-go-page="dashboard"]').length).toBe(1);
  });
});
