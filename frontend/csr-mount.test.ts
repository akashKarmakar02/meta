// F08 tests: mountCsrPage mounts the client page named by a validated CSR
// envelope through the generated registry and E04 lifecycle utility —
// correct component per clientEntry, old page unmounted cleanly, no
// duplicate instances, safe failures for unknown entries and bad targets.
import { describe, it, expect, beforeEach } from 'vitest';
import { mountCsrPage, CsrMountError, PageRootSelector } from './csr-mount';
import type { NavigationResponse } from './types';
import csrFixture from '../tests/fixtures/navigation-csr.json';

/**
 * A dashboard envelope. The pinned CSR fixture describes /dashboard/settings,
 * so tests name their clientEntry explicitly — this default keeps the
 * dashboard (the F08 acceptance route) front and center.
 */
function csrEnvelope(
  overrides?: Partial<NavigationResponse>
): NavigationResponse {
  const base = csrFixture as NavigationResponse;
  return {
    ...base,
    url: '/dashboard',
    routeId: 'dashboard/page.svelte',
    clientEntry: 'dashboard/page.svelte',
    title: 'Dashboard',
    ...overrides,
  };
}

/** The settings envelope verbatim from the pinned fixture. */
function settingsEnvelope(): NavigationResponse {
  return { ...(csrFixture as NavigationResponse) };
}

function installSSRDocument(pageHTML: string): void {
  document.body.innerHTML = `
    <div data-go-page-outlet>${pageHTML}</div>
    <footer data-go-site-footer>global footer</footer>
  `;
}

/** The shell a CSR direct load serves (E10). */
function installShellDocument(route = '/dashboard'): void {
  document.body.innerHTML = `
    <div data-go-page-outlet>
      <div data-go-page-root data-go-route="${route}" data-go-route-id="dashboard/page.svelte"></div>
    </div>
  `;
}

describe('mountCsrPage (F08)', () => {
  beforeEach(() => {
    installShellDocument('/dashboard');
  });

  it('mounts the Dashboard component from a dashboard envelope', async () => {
    const handle = await mountCsrPage(csrEnvelope());
    expect(handle.mounted).toBe(true);
    expect(handle.pageRoot.matches(PageRootSelector)).toBe(true);
    const root = document.querySelector(PageRootSelector) as HTMLElement;
    expect(root.querySelector('[data-go-page="dashboard"]')).not.toBeNull();
    expect(root.textContent).toContain('Count: 0');
    expect(root.querySelector('button')).not.toBeNull();
  });

  it('mounts the Settings component from a settings envelope', async () => {
    const handle = await mountCsrPage(csrEnvelope({
      url: '/dashboard/settings',
      routeId: 'dashboard/settings/page.svelte',
      clientEntry: 'dashboard/settings/page.svelte',
      title: 'Settings',
    }));
    expect(handle.mounted).toBe(true);
    const root = document.querySelector(PageRootSelector) as HTMLElement;
    expect(root.querySelector('h1')?.textContent).toBe('Settings');
    expect(root.textContent).toContain('Settings');
  });

  it('unmounts the old page and removes its content on transition', async () => {
    const first = await mountCsrPage(csrEnvelope());
    const root = document.querySelector(PageRootSelector) as HTMLElement;
    expect(root.textContent).toContain('Count: 0');

    const second = await mountCsrPage(
      csrEnvelope({
        url: '/dashboard/settings',
        routeId: 'dashboard/settings/page.svelte',
        clientEntry: 'dashboard/settings/page.svelte',
        title: 'Settings',
      }),
      first
    );
    await first.unmount(); // idempotent — already unmounted by the transition
    expect(second.mounted).toBe(true);
    // Old counter gone exactly; new page present exactly once.
    expect(root.querySelectorAll('[data-go-page="dashboard"]')).toHaveLength(0);
    expect(root.querySelectorAll('h1')).toHaveLength(1);
    expect(root.querySelector('h1')?.textContent).toBe('Settings');
    expect(second.pageRoot).toBe(first.pageRoot); // same target reused
  });

  it('does not duplicate mounted components across repeated navigations', async () => {
    let handle = await mountCsrPage(csrEnvelope());
    for (let i = 0; i < 3; i++) {
      handle = await mountCsrPage(
        csrEnvelope({
          url: '/dashboard/settings',
          routeId: 'dashboard/settings/page.svelte',
          clientEntry: 'dashboard/settings/page.svelte',
          title: 'Settings',
        }),
        handle
      );
    }
    const root = document.querySelector(PageRootSelector) as HTMLElement;
    expect(root.querySelectorAll('[data-go-page="dashboard"]')).toHaveLength(0);
    expect(root.querySelectorAll('h1')).toHaveLength(1);
    // The page does not supply a visual layout of its own.
    expect(root.querySelectorAll('[data-go-page="dashboard/settings"]')).toHaveLength(1);
    expect(root.querySelectorAll('[data-go-site-nav]')).toHaveLength(0);
  });

  it('creates a page root inside the F07 outlet when entering CSR from an SSR page', async () => {
    installSSRDocument('<main data-go-page-about><h1>About</h1></main>');
    const handle = await mountCsrPage(csrEnvelope());
    expect(handle.mounted).toBe(true);

    const outlet = document.querySelector('[data-go-page-outlet]') as HTMLElement;
    // SSR content replaced by the shell convention: outlet > page-root > page
    expect(outlet.querySelector('[data-go-page-about]')).toBeNull();
    const pageRoot = outlet.querySelector(PageRootSelector) as HTMLElement;
    expect(pageRoot).not.toBeNull();
    expect(pageRoot.getAttribute('data-go-route')).toBe(csrEnvelope().url);
    expect(pageRoot.getAttribute('data-go-route-id')).toBe(csrEnvelope().routeId);
    expect(pageRoot.querySelector('[data-go-page="dashboard"]')).not.toBeNull();
    // Everything outside the outlet is untouched.
    expect(document.querySelector('[data-go-site-footer]')?.textContent).toContain('global footer');
  });

  it('rejects unknown clientEntry values before any DOM change', async () => {
    installSSRDocument('<main data-go-page-about><h1>About</h1></main>');
    const bad = csrEnvelope({ clientEntry: 'no/such/page.svelte' });
    await expect(mountCsrPage(bad)).rejects.toBeInstanceOf(CsrMountError);
    // SSR content untouched — failure left no trace.
    expect(document.querySelector('[data-go-page-about] h1')?.textContent).toBe('About');
    expect(document.querySelector(PageRootSelector)).toBeNull();
  });

  it('rejects SSR envelopes — outlet replacement is F07', async () => {
    await expect(
      mountCsrPage(csrEnvelope({ mode: 'ssr', clientEntry: null }))
    ).rejects.toThrow(/mode "csr"/);
    // Nothing was mounted.
    expect(document.querySelector(`${PageRootSelector} > *`)).toBeNull();
  });

  it('handles a missing mount target and outlet safely', async () => {
    document.body.innerHTML = '<main>no shell, no outlet</main>';
    await expect(mountCsrPage(csrEnvelope())).rejects.toThrow(CsrMountError);
    await expect(mountCsrPage(csrEnvelope())).rejects.toThrow(/no \[data-go-page-outlet\]/);
    // No stray page roots were invented.
    expect(document.querySelectorAll(PageRootSelector)).toHaveLength(0);
  });

  it('handles ambiguous mount targets safely', async () => {
    document.body.innerHTML = `
      <div data-go-page-outlet>
        <div data-go-page-root></div>
        <div data-go-page-root></div>
      </div>
    `;
    await expect(mountCsrPage(csrEnvelope())).rejects.toThrow(/ambiguous mount target/);
  });

  it('keeps the target reusable after unmount', async () => {
    const first = await mountCsrPage(csrEnvelope());
    await first.unmount();
    const second = await mountCsrPage(csrEnvelope());
    expect(second.mounted).toBe(true);
    const root = document.querySelector(PageRootSelector) as HTMLElement;
    expect(root.querySelectorAll('[data-go-page="dashboard"]')).toHaveLength(1);
  });

  it('hands the envelope data to the page as a prop before mount (C04)', async () => {
    // The dashboard/[teamId] page renders the server-defined greeting, which
    // cannot be derived from the URL — proving the value came from the
    // envelope's data field, not window.location.
    const env = csrEnvelope({
      url: '/dashboard/team-42',
      routeId: 'dashboard/[teamId]/page.svelte',
      clientEntry: 'dashboard/[teamId]/page.svelte',
      title: 'Team',
      data: { teamId: 'team-42', title: 'Team team-42', greeting: 'Loaded by Go' },
    });
    const handle = await mountCsrPage(env);
    expect(handle.mounted).toBe(true);
    const root = document.querySelector(PageRootSelector) as HTMLElement;
    expect(root.querySelector('[data-go-page="dashboard-team"]')).not.toBeNull();
    expect(root.textContent).toContain('Loaded by Go');
    expect(root.textContent).toContain('Team team-42');
  });

  it('renders the no-data fallback when the envelope has no data (C04)', async () => {
    const env = csrEnvelope({
      url: '/dashboard/team-42',
      routeId: 'dashboard/[teamId]/page.svelte',
      clientEntry: 'dashboard/[teamId]/page.svelte',
      title: 'Team',
    });
    const handle = await mountCsrPage(env);
    expect(handle.mounted).toBe(true);
    const root = document.querySelector(PageRootSelector) as HTMLElement;
    expect(root.querySelector('[data-go-no-data]')).not.toBeNull();
  });
});
