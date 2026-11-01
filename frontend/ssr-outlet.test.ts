// F07 tests: replacePageOutlet swaps the server-owned page region from a
// validated SSR envelope (blueprint 5.2 step 9). Fragments come from the
// pinned fixture and from realistic templ output — the same bytes the Go
// server sends in envelopes.
import { describe, it, expect, beforeEach } from 'vitest';
import {
  replacePageOutlet,
  OutletError,
  PageOutletSelector,
} from './ssr-outlet';
import type { NavigationResponse } from './types';
import ssrFixture from '../tests/fixtures/navigation-ssr.json';

/** Full document shape as app/layout.templ renders it, with outlet. */
function installSSRDocument(pageHTML: string): void {
  document.body.innerHTML = `
    <header data-go-site-header><nav>global nav</nav></header>
    <div data-go-page-outlet>${pageHTML}</div>
    <footer data-go-site-footer>global footer</footer>
  `;
}

function ssrEnvelope(overrides?: Partial<NavigationResponse>): NavigationResponse {
  const env = { ...ssrFixture } as NavigationResponse;
  return { ...env, mode: 'ssr', clientEntry: null, ...overrides };
}

describe('replacePageOutlet (F07)', () => {
  beforeEach(() => {
    installSSRDocument('<main data-go-page="home"><h1>go-meta</h1></main>');
  });

  it('replaces the outlet content with the envelope fragment', () => {
    const outlet = replacePageOutlet(ssrEnvelope());
    expect(outlet.querySelector('h1')?.textContent).toBe('About');
    expect(outlet.querySelector('[data-go-page-about]')).not.toBeNull();
  });

  it('removes existing page content', () => {
    replacePageOutlet(ssrEnvelope());
    expect(document.querySelector('[data-go-page="home"]')).toBeNull();
    expect(document.body.textContent).not.toContain('go-meta framework root page');
  });

  it('places new content exactly once', () => {
    replacePageOutlet(ssrEnvelope());
    expect(document.querySelectorAll('[data-go-page-about]')).toHaveLength(1);
    expect(document.querySelectorAll('h1')).toHaveLength(1);
  });

  it('does not duplicate nodes after repeated replacements', () => {
    for (let i = 0; i < 3; i++) {
      replacePageOutlet(ssrEnvelope());
    }
    expect(document.querySelectorAll('[data-go-page-about]')).toHaveLength(1);
    // A different page afterwards also lands exactly once.
    replacePageOutlet(
      ssrEnvelope({
        html: '<main data-go-page-docs><h1>Docs</h1></main>',
      })
    );
    expect(document.querySelectorAll('[data-go-page-docs]')).toHaveLength(1);
    expect(document.querySelectorAll('main')).toHaveLength(1);
    expect(document.querySelector('[data-go-page-about]')).toBeNull();
    expect(document.querySelector('[data-go-page-docs]')?.textContent).toContain('Docs');
  });

  it('preserves root document structure and content outside the outlet', () => {
    const header = document.querySelector('[data-go-site-header]') as HTMLElement;
    const footer = document.querySelector('[data-go-site-footer]') as HTMLElement;
    const htmlEl = document.documentElement;
    const headCount = document.querySelectorAll('head').length;

    replacePageOutlet(ssrEnvelope());

    expect(document.querySelectorAll('head')).toHaveLength(headCount);
    expect(document.querySelectorAll('html')).toHaveLength(1);
    expect(document.querySelectorAll('body')).toHaveLength(1);
    // Outside-outlet nodes are THE SAME node objects, not re-created.
    expect(document.querySelector('[data-go-site-header]')).toBe(header);
    expect(document.querySelector('[data-go-site-footer]')).toBe(footer);
    expect(header.textContent).toContain('global nav');
    expect(footer.textContent).toContain('global footer');
  });

  it('handles a missing outlet explicitly', () => {
    document.body.innerHTML = '<main>no outlet here</main>';
    expect(() => replacePageOutlet(ssrEnvelope())).toThrow(OutletError);
    expect(() => replacePageOutlet(ssrEnvelope())).toThrow(/no \[data-go-page-outlet\]/);
    // The untouched main survives.
    expect(document.querySelector('main')?.textContent).toBe('no outlet here');
  });

  it('handles ambiguous outlets explicitly', () => {
    document.body.innerHTML = `
      <div data-go-page-outlet><p>a</p></div>
      <div data-go-page-outlet><p>b</p></div>
    `;
    expect(() => replacePageOutlet(ssrEnvelope())).toThrow(OutletError);
    expect(() => replacePageOutlet(ssrEnvelope())).toThrow(/ambiguous/);
    // Nothing changed on failure.
    expect(document.querySelectorAll('[data-go-page-outlet] p')[0].textContent).toBe('a');
    expect(document.querySelectorAll('[data-go-page-outlet] p')[1].textContent).toBe('b');
  });

  it('rejects CSR envelopes — mounting is F08, not outlet replacement', () => {
    expect(() =>
      replacePageOutlet(ssrEnvelope({ mode: 'csr', clientEntry: 'dashboard/page.svelte' }))
    ).toThrow(OutletError);
    expect(() =>
      replacePageOutlet(ssrEnvelope({ mode: 'csr', clientEntry: 'dashboard/page.svelte' }))
    ).toThrow(/mode "ssr"/);
    // Original content untouched after the rejection.
    expect(document.querySelector('[data-go-page="home"]')).not.toBeNull();
  });

  it('empties the outlet for an empty fragment without error', () => {
    const outlet = replacePageOutlet(ssrEnvelope({ html: '' }));
    expect(outlet.children).toHaveLength(0);
    // And the operation remains reversible afterwards.
    replacePageOutlet(ssrEnvelope());
    expect(outlet.querySelector('h1')?.textContent).toBe('About');
  });

  it('inserts scripts inertly — response scripts never execute', async () => {
    let executed = false;
    (window as unknown as Record<string, unknown>)['goMetaProbe'] = () => {
      executed = true;
    };
    replacePageOutlet(
      ssrEnvelope({
        html: '<main>x</main><script>goMetaProbe();</script>',
      })
    );
    // Template-parsed scripts are inert; give any queued microtask a beat.
    await Promise.resolve();
    expect(executed).toBe(false);
    // The script node exists but is not executable.
    const script = document.querySelector('script');
    expect(script).not.toBeNull();
  });

  it('inserts nested server markup intact (realistic templ output)', () => {
    // Shape matches BarePage() output for /docs wrapped by docs layout:
    // soft navigation replaces only the page, nested layouts persist.
    const nested = `
      <section data-go-layout-docs>
        <main data-go-page-docs><h1>Docs</h1><p>Documentation</p></main>
      </section>`;
    replacePageOutlet(ssrEnvelope({ html: nested }));
    expect(document.querySelector('[data-go-layout-docs]')).not.toBeNull();
    expect(document.querySelector('[data-go-page-docs] h1')?.textContent).toBe('Docs');
  });

  it('validates the real pinned fixture html through the full path', () => {
    // The fixture is the exact live envelope of /about; running it through
    // replacePageOutlet must produce that markup inside the outlet.
    // Compare via parsed nodes, not raw strings: DOM serialization
    // normalizes attribute quoting (data-go-page-about=""), so string
    // equality would test the serializer, not the insertion.
    replacePageOutlet(ssrFixture as NavigationResponse);
    const outlet = document.querySelector(PageOutletSelector) as HTMLElement;
    const probe = document.createElement('template');
    probe.innerHTML = (ssrFixture as NavigationResponse).html;
    // Node count and heading text prove structure survived exactly.
    expect(outlet.childNodes.length).toBe(probe.content.childNodes.length);
    expect(outlet.querySelector('h1')?.textContent).toBe(
      probe.content.querySelector('h1')?.textContent
    );
    expect(outlet.querySelector('[data-go-page-about]')).not.toBeNull();
    expect(outlet.querySelector('p')?.textContent).toBe(
      probe.content.querySelector('p')?.textContent
    );
  });
});
