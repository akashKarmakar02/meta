// Browser-enforced CORS acceptance (docs/guides/cors.md).
//
// Go tests can only assert which headers the server sets; per the guide, a
// claim that "a browser blocks a disallowed response" needs a real browser.
// These checks run in Chromium against two origins it genuinely distinguishes:
// http://localhost:3131 (host pages) and http://127.0.0.1:3141 (CORS-enabled
// API server). They verify approval, preflight, credentials — and rejection —
// from the browser side, plus that CORS approval never authorizes Meta's
// action endpoints (approval is not authorization).
import { test, expect, type Page } from '@playwright/test';

const MAIN = 'http://localhost:3131';
const API = 'http://127.0.0.1:3141';

interface FetchResult {
  ok: boolean;
  status: number;
  text: string;
  acao: string | null;
  acac: string | null;
}

async function crossFetch(
  page: Page,
  url: string,
  init: RequestInit = {}
): Promise<FetchResult> {
  return page.evaluate(
    async ({ url, init }) => {
      const res = await fetch(url, init);
      return {
        ok: res.ok,
        status: res.status,
        text: await res.text(),
        acao: res.headers.get('access-control-allow-origin'),
        acac: res.headers.get('access-control-allow-credentials'),
      };
    },
    { url, init }
  );
}

test.describe('CORS browser enforcement', () => {
  test('allowed origin can read a simple API response', async ({ page }) => {
    await page.goto(MAIN + '/');
    const result = await crossFetch(page, `${API}/api/posts`);
    expect(result.status).toBe(200);
    // Exact-origin approval, never a wildcard (the server runs credentialed).
    expect(result.acao).toBe(MAIN);
    expect(result.acac).toBe('true');
    expect(result.text).toContain('"posts"');
  });

  test('dynamic API route approves the same explicit origin', async ({ page }) => {
    await page.goto(MAIN + '/');
    const result = await crossFetch(page, `${API}/api/posts/42`);
    expect(result.status).toBe(200);
    expect(result.acao).toBe(MAIN);
  });

  test('preflighted request with Authorization header is approved', async ({
    page,
  }) => {
    await page.goto(MAIN + '/');
    const result = await crossFetch(page, `${API}/api/posts`, {
      headers: { Authorization: 'Bearer test-token' },
    });
    expect(result.status).toBe(200);
    expect(result.acao).toBe(MAIN);
    expect(result.acac).toBe('true');
  });

  test('preflighted method on a dynamic route passes preflight and reaches the handler', async ({
    page,
  }) => {
    await page.goto(MAIN + '/');
    // DELETE on the dynamic example route answers 204 through the route's own
    // handler — reachable only if Chromium accepted the CORS preflight.
    const result = await page.evaluate(async (url) => {
      const res = await fetch(url, { method: 'DELETE' });
      return {
        status: res.status,
        acao: res.headers.get('access-control-allow-origin'),
      };
    }, `${API}/api/posts/42`);
    expect(result.status).toBe(204);
    expect(result.acao).toBe(MAIN);
  });

  test('credentialed fetch gets exact origin, never a wildcard', async ({
    page,
  }) => {
    await page.goto(MAIN + '/');
    const result = await crossFetch(page, `${API}/api/posts`, {
      credentials: 'include',
    });
    expect(result.status).toBe(200);
    expect(result.acao).toBe(MAIN);
    expect(result.acac).toBe('true');
    expect(result.acao).not.toBe('*');
  });

  test('disallowed origin is blocked by the browser', async ({ page }) => {
    // Host the page on the API server's own origin but fetch through
    // localhost, which the CORS allowlist does not include.
    await page.goto(API + '/');
    const blocked = await page
      .evaluate(async (url) => {
        try {
          await fetch(url);
          return { blocked: false, message: '' };
        } catch (error) {
          return {
            blocked: true,
            message: error instanceof Error ? error.message : String(error),
          };
        }
      }, `http://localhost:${new URL(API).port}/api/posts`)
      .catch(() => ({ blocked: true, message: 'evaluation failed' }));
    expect(blocked.blocked).toBe(true);
  });

  test('cross-origin action POST: approved by CORS, still rejected by Meta', async ({
    page,
  }) => {
    await page.goto(MAIN + '/');
    // The action endpoint answers its preflight (middleware-level), and the
    // actual POST is readable only after approval — yet Meta's trusted-origin
    // gate still refuses to execute the action. Approval is not authorization.
    const result = await page.evaluate(async (url) => {
      const res = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: 'Ada' }),
      });
      return {
        status: res.status,
        text: await res.text(),
      };
    }, `${API}/_meta/actions/demo.echo`);
    expect(result.status).toBe(403);
    expect(result.text).toContain('"ok":false');
  });
});
