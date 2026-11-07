// fetchNavigation: browser-side request for the soft-navigation JSON
// envelope (blueprint 5.1/5.2 step 5-6). F06 scope: FETCH and VALIDATE
// only — no DOM changes, no Svelte mounting, no history writes, no
// fallback navigation (the caller decides how to react to failures).
//
// Everything that can go wrong surfaces as a typed error so later tasks
// (F12) can implement the window.location.assign fallback; nothing here
// throws opaque strings.

import {
  NAVIGATION_HEADER,
  NAVIGATION_VERSION,
  type NavigationMode,
  type NavigationResponse,
} from './types';

/** Discriminant for every failure mode of fetchNavigation. */
export type NavigationErrorCode =
  | 'aborted' // request cancelled via AbortSignal
  | 'http-error' // non-2xx status
  | 'bad-content-type' // 200 but not application/json (degraded HTML doc)
  | 'malformed-json' // body did not parse as JSON
  | 'cross-origin' // redirect left the origin, or final response is foreign
  | 'bad-envelope'; // parsed JSON is not a valid NavigationResponse v1

/** Typed failure surfaced to the caller (blueprint 5.2 step 12). */
export class NavigationError extends Error {
  readonly code: NavigationErrorCode;
  /** HTTP status when the error came from a response, else undefined. */
  readonly status?: number;
  /** Final response URL when it differs from the requested one (redirects). */
  readonly responseURL?: string;

  constructor(
    code: NavigationErrorCode,
    message: string,
    opts?: { status?: number; responseURL?: string }
  ) {
    super(message);
    this.name = 'NavigationError';
    this.code = code;
    this.status = opts?.status;
    this.responseURL = opts?.responseURL;
  }
}

/** Request options for fetchNavigation. */
export interface FetchNavigationOptions {
  /** AbortSignal forwarded to fetch; cancels the in-flight request. */
  signal?: AbortSignal;
}

/**
 * Fetch the soft-navigation envelope for a same-origin URL.
 *
 * Sends GET with `Accept: application/json` and `X-Go-Web-Navigation: 1`
 * (5.2 step 5), follows redirects with the browser's default policy, and
 * validates the response before returning it (5.2 step 6): status,
 * content type, JSON shape, protocol version, and per-mode field rules.
 *
 * Redirect policy (5.1): same-origin redirects are followed transparently
 * — the browser follows them and `response.url` reports the final URL. A
 * cross-origin final response is rejected (`cross-origin`): per the
 * blueprint it must trigger hard browser navigation, which the caller
 * owns, not a silent treatment of foreign data as valid.
 */
export async function fetchNavigation(
  url: string | URL,
  options?: FetchNavigationOptions
): Promise<NavigationResponse> {
  // Fragments are client-only (scroll targets) and never belong on the
  // wire; strip them instead of relying on every environment's fetch to.
  const requestURL = url instanceof URL ? url : new URL(url, document.baseURI);
  requestURL.hash = '';

  let response: Response;
  try {
    response = await fetch(requestURL.href, {
      method: 'GET',
      headers: {
        Accept: 'application/json',
        [NAVIGATION_HEADER]: String(NAVIGATION_VERSION),
      },
      signal: options?.signal,
    });
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') {
      throw new NavigationError('aborted', `navigation request aborted: ${url}`);
    }
    // Network failure (offline, refused connection) — rethrow as-is so
    // TypeError identity survives for callers distinguishing it.
    throw err;
  }

  // Cross-origin final response (via redirect): never consumed as data.
  // Browsers set response.url to the final (post-redirect) request URL;
  // environments without redirect tracking report "" and there is nothing
  // to check — same-origin is already guaranteed by the request itself.
  if (response.url !== '' && response.url !== 'about:blank') {
    const finalOrigin = new URL(response.url).origin;
    if (finalOrigin !== window.location.origin) {
      throw new NavigationError(
        'cross-origin',
        `navigation redirected off-origin: ${response.url}`,
        { responseURL: response.url }
      );
    }
  }

  // Non-2xx statuses are explicit failures (5.2 step 6).
  if (!response.ok) {
    throw new NavigationError(
      'http-error',
      `navigation request failed: ${response.status} ${response.statusText}`,
      { status: response.status, responseURL: response.url }
    );
  }

  // Content type gates parsing: a 200 text/html document means protocol
  // degradation (unsupported version) or a non-navigation endpoint —
  // either way it is not an envelope and must not be parsed as one.
  const contentType = response.headers.get('Content-Type') ?? '';
  const mediaType = contentType.split(';')[0].trim().toLowerCase();
  if (mediaType !== 'application/json') {
    throw new NavigationError(
      'bad-content-type',
      `navigation response is ${mediaType || 'unknown'}, not application/json`,
      { status: response.status, responseURL: response.url }
    );
  }

  let parsed: unknown;
  try {
    parsed = await response.json();
  } catch (err) {
    throw new NavigationError(
      'malformed-json',
      `navigation response is not valid JSON: ${(err as Error).message}`
    );
  }

  return validateNavigationResponse(parsed, response.url);
}

/**
 * Runtime validation of a parsed envelope (5.2 step 6: TypeScript typing
 * alone is not runtime validation). Throws NavigationError('bad-envelope')
 * describing the first violated rule.
 */
function validateNavigationResponse(parsed: unknown, sourceURL: string): NavigationResponse {
  const fail = (detail: string): never => {
    throw new NavigationError(
      'bad-envelope',
      `invalid navigation envelope from ${sourceURL}: ${detail}`
    );
  };

  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    fail('not a JSON object');
  }
  const env = parsed as Record<string, unknown>;

  if (env.version !== NAVIGATION_VERSION) {
    fail(`unsupported version ${JSON.stringify(env.version)}`);
  }
  if (typeof env.url !== 'string' || env.url === '') {
    fail('missing or invalid url');
  }
  if (typeof env.routeId !== 'string' || env.routeId === '') {
    fail('missing or invalid routeId');
  }
  if (env.mode !== 'ssr' && env.mode !== 'csr') {
    fail(`invalid mode ${JSON.stringify(env.mode)}`);
  }
  if (typeof env.title !== 'string') {
    fail('missing or invalid title');
  }
  if (env.metadata !== undefined) {
    if (typeof env.metadata !== 'object' || env.metadata === null || Array.isArray(env.metadata)) {
      fail('metadata must be an object');
    }
    const meta = env.metadata as Record<string, unknown>;
    const validateFields = (value: Record<string, unknown>, names: string[]) => {
      for (const name of names) {
        if (value[name] !== undefined && typeof value[name] !== 'string') fail('invalid metadata fields');
      }
    };
    validateFields(meta, ['title', 'description', 'canonical']);
    for (const [name, fields] of [
      ['openGraph', ['title', 'description', 'image', 'url', 'type', 'siteName']],
      ['twitter', ['card', 'title', 'description', 'image', 'site']],
    ] as const) {
      const value = meta[name];
      if (value !== undefined) {
        if (typeof value !== 'object' || value === null || Array.isArray(value)) fail('invalid metadata fields');
        validateFields(value as Record<string, unknown>, [...fields]);
      }
    }
  }
  if (!Array.isArray(env.layouts) || env.layouts.some((l) => typeof l !== 'string')) {
    fail('layouts must be an array of strings');
  }
  if ((env.layouts as unknown[]).length > 1) {
    fail('at most one visual layout is supported');
  }
  if (typeof env.html !== 'string') {
    fail('missing or invalid html');
  }

  // SSR has no client entry. CSR has an entry and may include a
  // server-rendered templ visual layout with its page mount point.
  if (env.mode === 'ssr' && env.clientEntry !== null && env.clientEntry !== undefined) {
    fail('ssr envelope must not carry clientEntry');
  }
  if (env.mode === 'csr') {
    if (typeof env.clientEntry !== 'string' || env.clientEntry === '') {
      fail('csr envelope requires a clientEntry route ID');
    }
  }

  // Every field has been checked above; the assertions only convert the
  // unknown-typed JSON view into the declared response type.
  //
  // The optional C04 `data` payload is passed through VERBATIM. It is the
  // loader's JSON result: any JSON value is legal (object, array, string,
  // number, boolean, or null), so there is nothing to validate beyond the
  // JSON parse that already happened — and dropping it here would sever
  // the server's loader delivery from every soft-navigation mount
  // (csr-mount, layout-child-swap, and the pipeline all read env.data).
  // Absence stays absence: `undefined` means "no registered loader" and a
  // JSON null stays null, keeping the two states distinguishable downstream.
  return {
    version: env.version as number,
    url: env.url as string,
    routeId: env.routeId as string,
    mode: env.mode as NavigationMode,
    title: env.title as string,
    ...(env.metadata !== undefined ? { metadata: env.metadata as NavigationResponse['metadata'] } : {}),
    layouts: env.layouts as string[],
    html: env.html as string,
    clientEntry:
      env.mode === 'csr' ? (env.clientEntry as string) : null,
    ...(env.data !== undefined ? { data: env.data } : {}),
  };
}
