// F06 tests: fetchNavigation sends the soft-navigation request and
// validates the response (blueprint 5.2 steps 5-6) — headers, status,
// content type, JSON shape, version, and per-mode field rules. Failures
// surface as typed NavigationErrors so later tasks can hard-navigate.
//
// fetch is mocked per-test (vi.stubGlobal) to control status, content
// type, body, redirected URL, and aborts. A separate integration check
// exercises the real Go server envelopes against fetchNavigation's
// validation via the pinned fixtures.
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import {
  fetchNavigation,
  NavigationError,
  type FetchNavigationOptions,
} from './navigation';
import { NAVIGATION_HEADER, NAVIGATION_VERSION } from './types';
import ssrFixture from '../tests/fixtures/navigation-ssr.json';
import csrFixture from '../tests/fixtures/navigation-csr.json';

function jsonResponse(
  body: unknown,
  init?: { status?: number; contentType?: string; url?: string }
): Response {
  const status = init?.status ?? 200;
  const headers = new Headers({
    'Content-Type': init?.contentType ?? 'application/json',
  });
  const response = new Response(
    typeof body === 'string' ? body : JSON.stringify(body),
    { status, headers }
  );
  if (init?.url) {
    // ResponseInit has no url field; real fetch sets response.url from the
    // final (post-redirect) request URL. Simulate redirects by patching it.
    Object.defineProperty(response, 'url', { value: init.url });
  }
  return response;
}

/** Install a programmable fetch mock; captured calls exposed for asserts. */
function mockFetch(impl: (input: RequestInfo | URL, init?: RequestInit) => Response | Promise<Response>) {
  const calls: Array<{ url: string; init: RequestInit }> = [];
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({
      url: typeof input === 'string' ? input : input instanceof URL ? input.href : input.url,
      init: init ?? {},
    });
    return impl(input, init);
  });
  vi.stubGlobal('fetch', fn);
  return { calls, fn };
}

describe('fetchNavigation (F06)', () => {
  beforeEach(() => {
    window.location.href = 'http://localhost/';
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('sends GET with Accept application/json and the navigation header', async () => {
    const { calls } = mockFetch(() => jsonResponse(ssrFixture));
    await fetchNavigation('/about');
    expect(calls).toHaveLength(1);
    // Relative URLs are normalized absolute against the document base.
    expect(calls[0].url).toBe('http://localhost/about');
    expect(calls[0].init.method).toBe('GET');
    const headers = new Headers(calls[0].init.headers as HeadersInit);
    expect(headers.get('Accept')).toBe('application/json');
    expect(headers.get(NAVIGATION_HEADER)).toBe(String(NAVIGATION_VERSION));
  });

  it('preserves query parameters of the requested URL', async () => {
    const { calls } = mockFetch(() => jsonResponse(ssrFixture));
    const url = new URL('/products/42?tab=specs&x=a%20b#reviews', 'http://localhost/');
    await fetchNavigation(url);
    // Fragment is client-only and never sent to the server.
    expect(calls[0].url).toBe('http://localhost/products/42?tab=specs&x=a%20b');
  });

  it('parses a valid SSR envelope', async () => {
    mockFetch(() => jsonResponse(ssrFixture));
    const env = await fetchNavigation('/about');
    expect(env.version).toBe(1);
    expect(env.mode).toBe('ssr');
    expect(env.routeId).toBe('about/page.templ');
    expect(env.layouts).toEqual(['app/layout.templ']);
    expect(env.html).toContain('<h1>About</h1>');
    expect(env.clientEntry).toBeNull();
  });

  it('parses a valid CSR envelope', async () => {
    mockFetch(() => jsonResponse(csrFixture));
    const env = await fetchNavigation('/dashboard/settings');
    expect(env.mode).toBe('csr');
    expect(env.clientEntry).toBe('dashboard/settings/page.svelte');
    expect(env.html).toBe('');
  });

  // C06: the validator must pass the optional loader payload through —
  // every soft-navigation mount site (csr-mount, layout-child-swap, the
  // pipeline) reads env.data, so dropping it here would silently sever
  // C04's delivery for all genuine soft navigations.
  it('passes the optional CSR loader data field through verbatim', async () => {
    for (const payload of [
      { teamId: 'team-42', greeting: 'Loaded by Go' },
      null,
      'plain string',
      42,
      [1, 2, 3],
      false,
    ]) {
      mockFetch(() =>
        jsonResponse(JSON.stringify({ ...csrFixture, data: payload }))
      );
      const env = await fetchNavigation('/dashboard/settings');
      expect(env.data).toEqual(payload);
    }
  });

  it('keeps the loader data field absent when the server omits it', async () => {
    mockFetch(() => jsonResponse(csrFixture));
    const env = await fetchNavigation('/dashboard/settings');
    // Absence means "no registered loader"; it must stay distinct from a
    // JSON null payload both here and at the page boundary.
    expect('data' in env).toBe(false);
    expect(env.data).toBeUndefined();
  });

  it('forwards the AbortSignal to fetch', async () => {
    const controller = new AbortController();
    const { fn } = mockFetch(() => {
      throw new DOMException('The operation was aborted.', 'AbortError');
    });
    const promise = fetchNavigation('/about', { signal: controller.signal });
    controller.abort();
    await expect(promise).rejects.toMatchObject({
      name: 'NavigationError',
      code: 'aborted',
    });
    // The caller's signal reached fetch itself.
    expect((fn.mock.calls[0] as unknown[])[1]).toMatchObject({
      signal: controller.signal,
    });
  });

  it('rejects HTTP 404 and 500 responses explicitly', async () => {
    for (const status of [404, 500]) {
      mockFetch(() =>
        jsonResponse(JSON.stringify({ error: status }), {
          status,
          contentType: 'text/html',
        })
      );
      const err = await fetchNavigation('/missing').catch((e) => e);
      expect(err, `status ${status}`).toBeInstanceOf(NavigationError);
      expect(err.code, `status ${status}`).toBe('http-error');
      expect(err.status, `status ${status}`).toBe(status);
    }
  });

  it('rejects a 200 HTML document as degraded protocol, not an envelope', async () => {
    mockFetch(() =>
      jsonResponse('<!DOCTYPE html><html><body>full doc</body></html>', {
        contentType: 'text/html; charset=utf-8',
      })
    );
    const err = await fetchNavigation('/about').catch((e) => e);
    expect(err).toBeInstanceOf(NavigationError);
    expect(err.code).toBe('bad-content-type');
  });

  it('rejects malformed JSON', async () => {
    mockFetch(() => jsonResponse('{not json'));
    const err = await fetchNavigation('/about').catch((e) => e);
    expect(err).toBeInstanceOf(NavigationError);
    expect(err.code).toBe('malformed-json');
  });

  it('rejects unsupported protocol versions', async () => {
    for (const version of [2, 0, '1', null]) {
      mockFetch(() =>
        jsonResponse({ ...ssrFixture, version })
      );
      const err = await fetchNavigation('/about').catch((e) => e);
      expect(err, `version ${JSON.stringify(version)}`).toBeInstanceOf(NavigationError);
      expect(err.code, `version ${JSON.stringify(version)}`).toBe('bad-envelope');
    }
  });

  it('rejects invalid mode values', async () => {
    for (const mode of ['mixed', 'SSR', '', null, undefined]) {
      mockFetch(() => jsonResponse({ ...ssrFixture, mode }));
      const err = await fetchNavigation('/about').catch((e) => e);
      expect(err, `mode ${JSON.stringify(mode)}`).toBeInstanceOf(NavigationError);
      expect(err.code, `mode ${JSON.stringify(mode)}`).toBe('bad-envelope');
    }
  });

  it('rejects missing essential fields', async () => {
    for (const field of ['url', 'routeId', 'title', 'layouts', 'html']) {
      const broken: Record<string, unknown> = { ...ssrFixture };
      delete broken[field];
      mockFetch(() => jsonResponse(broken));
      const err = await fetchNavigation('/about').catch((e) => e);
      expect(err, `missing ${field}`).toBeInstanceOf(NavigationError);
      expect(err.code, `missing ${field}`).toBe('bad-envelope');
    }
  });

  it('rejects invalid layouts shape', async () => {
    for (const layouts of [null, 'app/layout.templ', [1], ['a', null]]) {
      mockFetch(() => jsonResponse({ ...ssrFixture, layouts }));
      const err = await fetchNavigation('/about').catch((e) => e);
      expect(err, `layouts ${JSON.stringify(layouts)}`).toBeInstanceOf(NavigationError);
      expect(err.code).toBe('bad-envelope');
    }
  });

  it('rejects SSR envelopes carrying a clientEntry', async () => {
    mockFetch(() =>
      jsonResponse({ ...ssrFixture, clientEntry: 'dashboard/page.svelte' })
    );
    const err = await fetchNavigation('/about').catch((e) => e);
    expect(err).toBeInstanceOf(NavigationError);
    expect(err.code).toBe('bad-envelope');
  });

  it('requires CSR clientEntry and accepts server-rendered visual layout markup', async () => {
    mockFetch(() => jsonResponse({ ...csrFixture, clientEntry: null }));
    const err = await fetchNavigation('/dashboard/settings').catch((e) => e);
    expect(err).toBeInstanceOf(NavigationError);
    expect(err.code).toBe('bad-envelope');

    mockFetch(() =>
      jsonResponse({ ...csrFixture, html: '<p>server html</p>' })
    );
    const response = await fetchNavigation('/dashboard/settings');
    expect(response.html).toBe('<p>server html</p>');
  });

  it('rejects non-object JSON payloads', async () => {
    for (const body of ['[]', '"string"', '42', 'null']) {
      mockFetch(() => jsonResponse(body));
      const err = await fetchNavigation('/about').catch((e) => e);
      expect(err, `body ${body}`).toBeInstanceOf(NavigationError);
      expect(err.code, `body ${body}`).toBe('bad-envelope');
    }
  });

  it('rejects cross-origin final responses (redirect off-origin)', async () => {
    mockFetch(() =>
      jsonResponse(ssrFixture, { url: 'https://elsewhere.example.com/about' })
    );
    const err = await fetchNavigation('/about').catch((e) => e);
    expect(err).toBeInstanceOf(NavigationError);
    expect(err.code).toBe('cross-origin');
    expect(err.responseURL).toBe('https://elsewhere.example.com/about');
  });

  it('accepts same-origin redirects (final response on-origin)', async () => {
    mockFetch(() =>
      jsonResponse(ssrFixture, { url: 'http://localhost/about-moved' })
    );
    const env = await fetchNavigation('/about');
    expect(env.routeId).toBe('about/page.templ');
  });

  it('passes network failures through unchanged (TypeError identity)', async () => {
    const networkErr = new TypeError('Failed to fetch');
    mockFetch(() => {
      throw networkErr;
    });
    const err = await fetchNavigation('/about').catch((e) => e);
    expect(err).toBe(networkErr);
  });

  it('validates the real checked-in fixtures as legal envelopes', async () => {
    // Fixtures are the pinned wire contract; the validator must accept
    // them byte-for-byte.
    mockFetch(() => jsonResponse(ssrFixture));
    const ssr = await fetchNavigation('/about');
    expect(ssr.title).toBe('About');

    mockFetch(() => jsonResponse(csrFixture));
    const csr = await fetchNavigation('/dashboard/settings');
    expect(csr.title).toBe('Settings');
  });
});

// Integration with the real Go server envelopes: optional — runs only
// when a server is listening on GO_META_TEST_SERVER (set by the developer)
// so CI without a live server still passes.
const serverBase = process.env['GO_META_TEST_SERVER'];
describe.skipIf(!serverBase)('fetchNavigation against live Go server', () => {
  beforeEach(() => {
    // Simulate being a page served BY that server — the real-world
    // same-origin situation soft navigation runs in.
    window.location.href = `${serverBase}/`;
  });

  it('validates real SSR and CSR envelopes from the server', async () => {
    for (const [path, mode, routeId] of [
      ['/about', 'ssr', 'about/page.templ'],
      ['/dashboard', 'csr', 'dashboard/page.svelte'],
    ] as const) {
      const env = await fetchNavigation(`${serverBase}${path}`);
      expect(env.mode).toBe(mode);
      expect(env.routeId).toBe(routeId);
      expect(env.version).toBe(NAVIGATION_VERSION);
    }
  });
});
