/** Minimal public ClerkJS surface; supply your application's initialized singleton. */
export interface RecoveryClerk {
  readonly loaded: boolean;
  readonly session: { getToken(options: { skipCache: true }): Promise<string | null> } | null | undefined;
}

export interface RecoveryOptions {
  getClerk(): Promise<RecoveryClerk>;
  returnTo: string;
  signInURL: string;
  recoveryURL: string;
  /** A same-origin, public endpoint using auth.RecoveryStatus(), after auth.Middleware(). */
  checkURL: string;
  signal?: AbortSignal;
  /** Bounds SDK initialization, refresh and the cookie check together; default 15s. */
  timeoutMs?: number;
}

export type RecoveryResult =
  | { status: 'ready'; destination: string }
  | { status: 'signed-out'; signInURL: string }
  | { status: 'error'; reason: 'invalid-destination' | 'initialization' | 'refresh' | 'cookie-check' | 'timeout' | 'aborted' };

function localURL(raw: string): URL {
  if (!raw.startsWith('/') || raw.startsWith('//') || raw.length > 4096 || /[\u0000-\u0020\u007f\\]/.test(raw)) throw new Error('invalid destination');
  const url = new URL(raw, window.location.origin);
  const path = decodeURIComponent(url.pathname);
  if (url.origin !== window.location.origin || path.startsWith('//') || /[\u0000-\u0020\u007f%\\]/.test(path)) throw new Error('invalid destination');
  return url;
}

/**
 * Recover via Clerk's supported getToken API, then recheck the actual cookie on
 * the server. Never reads/writes cookies, decodes tokens, or returns credentials.
 * Does not navigate or render UI. A timeout/abort prevents late SDK results from
 * resuming navigation; it cannot cancel work internal to ClerkJS.
 */
export async function recoverSession(options: RecoveryOptions): Promise<RecoveryResult> {
  let destination: URL;
  let signIn: URL;
  let check: URL;
  try {
    destination = localURL(options.returnTo);
    signIn = localURL(options.signInURL);
    check = localURL(options.checkURL);
    const recovery = localURL(options.recoveryURL);
    const path = (url: URL) => decodeURIComponent(url.pathname).replace(/\/+$/, '');
    if ([signIn, check, recovery].some((url) => path(url) === path(destination)) ||
      new Set([signIn, check, recovery].map(path)).size !== 3) throw new Error('recursive destination');
    signIn.searchParams.set('redirect_url', destination.pathname + destination.search + destination.hash);
  } catch { return { status: 'error', reason: 'invalid-destination' }; }

  if (options.signal?.aborted) return { status: 'error', reason: 'aborted' };
  const controller = new AbortController();
  let timedOut = false;
  let interrupt!: () => void;
  const interrupted = new Promise<never>((_, reject) => { interrupt = () => reject(new Error('interrupted')); });
  const onAbort = () => { controller.abort(); interrupt(); };
  options.signal?.addEventListener('abort', onAbort, { once: true });
  const timeout = options.timeoutMs ?? 15_000;
  const timer = setTimeout(() => { timedOut = true; onAbort(); }, Number.isFinite(timeout) && timeout > 0 ? timeout : 15_000);
  const wait = <T>(work: Promise<T>) => Promise.race([work, interrupted]);
  let stage: 'initialization' | 'refresh' | 'cookie-check' = 'initialization';
  try {
    const clerk = await wait(options.getClerk());
    if (!clerk.loaded || clerk.session === undefined) return { status: 'error', reason: 'initialization' };
    if (clerk.session === null) return { status: 'signed-out', signInURL: signIn.href };
    stage = 'refresh';
    const token = await wait(clerk.session.getToken({ skipCache: true }));
    // A null refresh result is not proof of sign-out; preserve the retry screen.
    if (!token) return { status: 'error', reason: 'refresh' };
    stage = 'cookie-check';
    const response = await wait(fetch(check.href, {
      method: 'GET', credentials: 'same-origin', cache: 'no-store',
      redirect: 'error', signal: controller.signal, headers: { Accept: 'application/json' },
    }));
    if (response.status !== 204) return { status: 'error', reason: 'cookie-check' };
    return { status: 'ready', destination: destination.pathname + destination.search + destination.hash };
  } catch {
    return { status: 'error', reason: timedOut ? 'timeout' : options.signal?.aborted ? 'aborted' : stage };
  } finally {
    clearTimeout(timer);
    options.signal?.removeEventListener('abort', onAbort);
  }
}
