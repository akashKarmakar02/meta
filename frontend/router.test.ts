// F05 tests: the same-origin anchor interceptor implements blueprint
// 5.2 steps 1-3 — document-level delegation, the full exclusion list,
// and preventDefault + handoff for eligible clicks. Happy-dom synthesizes
// events (isTrusted undefined), which the interceptor treats as trusted:
// production browsers set isTrusted false on synthetic events, and those
// stay native.
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import {
  installLinkInterceptor,
  uninstallLinkInterceptor,
  type LinkNavigation,
} from './router';

interface Intercepted {
  url: URL;
  destination: string;
  prevented: boolean;
}

function setup(): Intercepted[] {
  const calls: Intercepted[] = [];
  installLinkInterceptor((nav: LinkNavigation) => {
    calls.push({
      url: nav.url,
      destination: nav.destination,
      prevented: nav.event.defaultPrevented,
    });
  });
  return calls;
}

function makeAnchor(attrs: Record<string, string>, inner = 'link'): HTMLAnchorElement {
  const a = document.createElement('a');
  for (const [k, v] of Object.entries(attrs)) {
    a.setAttribute(k, v);
  }
  a.innerHTML = inner;
  document.body.appendChild(a);
  return a;
}

function click(el: Element, init: MouseEventInit = {}): MouseEvent {
  const event = new MouseEvent('click', {
    bubbles: true,
    cancelable: true,
    composed: true,
    button: 0,
    ...init,
  });
  el.dispatchEvent(event);
  return event;
}

describe('link interceptor (F05)', () => {
  beforeEach(() => {
    window.location.href = 'http://localhost/';
  });

  afterEach(() => {
    uninstallLinkInterceptor();
    document.body.innerHTML = '';
  });

  it('intercepts an eligible same-origin link', () => {
    const calls = setup();
    const a = makeAnchor({ href: '/about' });
    click(a);
    expect(calls).toHaveLength(1);
    expect(calls[0].url.href).toBe('http://localhost/about');
    expect(calls[0].destination).toBe('/about');
    expect(calls[0].prevented).toBe(true);
  });

  it('handles links added to the document after installation (delegation)', () => {
    const calls = setup();
    // Link created and inserted AFTER installLinkInterceptor ran.
    const a = makeAnchor({ href: '/dashboard' });
    click(a);
    expect(calls).toHaveLength(1);
    expect(calls[0].destination).toBe('/dashboard');
  });

  it('resolves clicks on nested elements inside an anchor', () => {
    const calls = setup();
    const a = makeAnchor({ href: '/about' }, '<span><em>deep</em></span>');
    click(a.querySelector('em') as Element);
    expect(calls).toHaveLength(1);
    expect(calls[0].url.href).toBe('http://localhost/about');
  });

  it('ignores clicks outside anchors entirely', () => {
    const calls = setup();
    const button = document.createElement('button');
    document.body.appendChild(button);
    const event = click(button);
    expect(calls).toHaveLength(0);
    expect(event.defaultPrevented).toBe(false);
  });

  it('keeps external origins native', () => {
    const calls = setup();
    for (const href of [
      'https://example.com/about',
      'http://localhost:3000/about',
      'https://google.com',
    ]) {
      const a = makeAnchor({ href });
      const event = click(a);
      expect(calls).toHaveLength(0);
      expect(event.defaultPrevented).toBe(false);
    }
  });

  it('keeps modifier clicks native (Ctrl/Cmd/Shift/Alt)', () => {
    const calls = setup();
    const cases: Array<[string, Partial<MouseEventInit>]> = [
      ['ctrlKey', { ctrlKey: true }],
      ['metaKey', { metaKey: true }],
      ['shiftKey', { shiftKey: true }],
      ['altKey', { altKey: true }],
    ];
    for (const [label, mods] of cases) {
      const a = makeAnchor({ href: '/about' });
      const event = click(a, mods);
      expect(calls, label).toHaveLength(0);
      expect(event.defaultPrevented, label).toBe(false);
    }
  });

  it('keeps non-primary button clicks native (middle/right/auxiliary)', () => {
    const calls = setup();
    for (const button of [1, 2, 3, 4]) {
      const a = makeAnchor({ href: '/about' });
      const event = click(a, { button });
      expect(calls, `button ${button}`).toHaveLength(0);
      expect(event.defaultPrevented, `button ${button}`).toBe(false);
    }
  });

  it('keeps synthetic (isTrusted=false) events native', () => {
    const calls = setup();
    const a = makeAnchor({ href: '/about' });
    const event = new MouseEvent('click', {
      bubbles: true,
      cancelable: true,
      button: 0,
    });
    Object.defineProperty(event, 'isTrusted', { value: false });
    a.dispatchEvent(event);
    expect(calls).toHaveLength(0);
    expect(event.defaultPrevented).toBe(false);
  });

  it('keeps target=_blank and other browsing contexts native', () => {
    const calls = setup();
    for (const target of ['_blank', '_parent', '_top', 'custom-window']) {
      const a = makeAnchor({ href: '/about', target });
      const event = click(a);
      expect(calls, `target=${target}`).toHaveLength(0);
      expect(event.defaultPrevented, `target=${target}`).toBe(false);
    }
  });

  it('treats target=_self (case-insensitive) as current context and intercepts', () => {
    const calls = setup();
    const a = makeAnchor({ href: '/about', target: '_SELF' });
    click(a);
    expect(calls).toHaveLength(1);
  });

  it('keeps download links native', () => {
    const calls = setup();
    const a = makeAnchor({ href: '/files/report.pdf', download: '' });
    const event = click(a);
    expect(calls).toHaveLength(0);
    expect(event.defaultPrevented).toBe(false);
  });

  it('keeps rel=external links native', () => {
    const calls = setup();
    const a = makeAnchor({ href: '/about', rel: 'noopener external' });
    const event = click(a);
    expect(calls).toHaveLength(0);
    expect(event.defaultPrevented).toBe(false);
  });

  it('keeps data-no-spa links native (explicit opt-out)', () => {
    const calls = setup();
    const a = makeAnchor({ href: '/about', 'data-no-spa': '' });
    const event = click(a);
    expect(calls).toHaveLength(0);
    expect(event.defaultPrevented).toBe(false);
  });

  it('keeps unsupported URL schemes native (mailto/tel/javascript/data)', () => {
    const calls = setup();
    for (const href of [
      'mailto:user@example.com',
      'tel:+15551234567',
      'javascript:alert(1)',
      'data:text/html,hi',
      'ftp://localhost/file',
    ]) {
      const a = makeAnchor({ href });
      const event = click(a);
      expect(calls, href).toHaveLength(0);
      expect(event.defaultPrevented, href).toBe(false);
    }
  });

  it('keeps fragment-only and same-document links native', () => {
    const calls = setup();
    window.location.href = 'http://localhost/dashboard/settings';
    for (const href of ['#section', '#', '', '/dashboard/settings#section']) {
      const a = makeAnchor({ href });
      const event = click(a);
      expect(calls, `href=${href}`).toHaveLength(0);
      expect(event.defaultPrevented, `href=${href}`).toBe(false);
    }
  });

  it('prevents a plain current-page click without starting a navigation', () => {
    const calls = setup();
    window.history.replaceState(null, '', '/dashboard?tab=summary');
    const a = makeAnchor({ href: '/dashboard?tab=summary' });
    const event = click(a);
    expect(event.defaultPrevented).toBe(true);
    expect(calls).toHaveLength(0);
  });

  it('soft-navigates to the current path when removing a fragment', () => {
    const calls = setup();
    window.history.replaceState(null, '', '/dashboard#details');
    const a = makeAnchor({ href: '/dashboard' });
    const event = click(a);
    expect(event.defaultPrevented).toBe(true);
    expect(calls).toHaveLength(1);
    expect(calls[0].destination).toBe('/dashboard');
  });

  it('intercepts a same-page URL that changes the query string', () => {
    const calls = setup();
    window.location.href = 'http://localhost/dashboard/settings';
    const a = makeAnchor({ href: '/dashboard/settings?page=2' });
    click(a);
    expect(calls).toHaveLength(1);
    expect(calls[0].destination).toBe('/dashboard/settings?page=2');
  });

  it('preserves query parameters, encoding, and hash in destination', () => {
    const calls = setup();
    const a = makeAnchor({ href: '/products/42?tab=specs&x=a%20b#reviews' });
    click(a);
    expect(calls).toHaveLength(1);
    expect(calls[0].destination).toBe('/products/42?tab=specs&x=a%20b#reviews');
    expect(calls[0].url instanceof URL).toBe(true);
    expect(calls[0].url.searchParams.get('x')).toBe('a b');
  });

  it('leaves already-prevented events untouched', () => {
    const calls = setup();
    const a = makeAnchor({ href: '/about' });
    const seen: string[] = [];
    // A listener registered before ours claims the event first (same-node
    // capture listeners run in registration order; ours was installed by
    // setup() above).
    const early = (e: Event) => {
      seen.push('other');
      e.preventDefault();
    };
    // To run strictly before ours, register on Window (outermost node).
    window.addEventListener('click', early, { capture: true });
    try {
      const event = click(a);
      expect(seen).toEqual(['other']);
      expect(calls).toHaveLength(0);
      expect(event.defaultPrevented).toBe(true); // by the other listener, not us
    } finally {
      window.removeEventListener('click', early, { capture: true });
    }
  });

  it('does not intercept invalid or unresolvable URLs', () => {
    const calls = setup();
    // href attribute that cannot resolve to a valid absolute URL.
    const a = document.createElement('a');
    a.setAttribute('href', 'http://[invalid');
    document.body.appendChild(a);
    const event = click(a);
    expect(calls).toHaveLength(0);
    expect(event.defaultPrevented).toBe(false);
  });

  it('repeated installation does not double-handle clicks', () => {
    const calls = setup();
    // Second install while the first is active: replaces the handler.
    const later: Intercepted[] = [];
    installLinkInterceptor((nav) => later.push({
      url: nav.url,
      destination: nav.destination,
      prevented: nav.event.defaultPrevented,
    }));
    const a = makeAnchor({ href: '/about' });
    click(a);
    expect(calls).toHaveLength(0); // old handler replaced, not stacked
    expect(later).toHaveLength(1);
    expect(later[0].destination).toBe('/about');

    // A fresh install after uninstall still handles exactly once.
    uninstallLinkInterceptor();
    const fresh = setup();
    click(a);
    expect(fresh).toHaveLength(1);
  });

  it('uninstalled interceptor never handles clicks', () => {
    const calls = setup();
    uninstallLinkInterceptor();
    const a = makeAnchor({ href: '/about' });
    const event = click(a);
    expect(calls).toHaveLength(0);
    expect(event.defaultPrevented).toBe(false);
  });
});
