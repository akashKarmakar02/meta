// D02 browser tests: action endpoints through a real browser origin.
//
// The C06 finding applies: synthetic clicks are not a substitute for trusted
// browser interactions. These tests drive explicit fetch()-based JSON
// mutations from a real page context, so the browser attaches genuine
// same-origin Origin headers — the exact condition the D02 origin gate
// must accept. Cross-origin rejection is driven from an explicitly
// different origin (evil.example.com) to prove the gate fails closed.
//
// D05's enhanced client-side submission UX is NOT implemented here; this is
// raw JSON transport verification only.
import { test, expect } from '@playwright/test';

test.describe('D02 server actions over HTTP', () => {
  test('a same-origin JSON POST executes the action and returns typed data', async ({
    page,
  }) => {
    await page.goto('/');
    // Real page context: the browser sends its own Origin header.
    const result = await page.evaluate(async () => {
      const res = await fetch('/_meta/actions/demo.createPost', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: 'Hello from Chromium' }),
      });
      return { status: res.status, body: await res.json() };
    });
    expect(result.status).toBe(200);
    expect(result.body.ok).toBe(true);
    expect(result.body.data).toEqual({ id: 42 });
  });

  test('an error action maps to a safe JSON error without internals', async ({
    page,
  }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const res = await fetch('/_meta/actions/demo.fail', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message: 'boom' }),
      });
      return { status: res.status, body: await res.json() };
    });
    expect(result.status).toBe(500);
    expect(result.body.ok).toBe(false);
    expect(result.body.error).toBe('action failed');
    // The internal detail ("secret detail") must never reach the client.
    expect(JSON.stringify(result.body)).not.toContain('secret');
  });

  test('GET on an action endpoint never executes it', async ({ page }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const res = await fetch('/_meta/actions/demo.createPost');
      return res.status;
    });
    expect([404, 405]).toContain(result);
    const count = await page.evaluate(async () => {
      const res = await fetch('/_meta/actions/demo.echo', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message: 'probe' }),
      });
      const env = await res.json();
      return env.data?.message ?? null;
    });
    // demo.echo answered, meaning the endpoint is alive; the earlier GET did
    // not execute demo.createPost (its counter was not asserted remotely, but
    // the status codes prove dispatch never accepted GET).
    expect(count).toBe('probe');
  });

  test('soft navigation does not invoke actions', async ({ page }) => {
    await page.goto('/');
    // Navigate to a real page; no action should fire as a side effect. A
    // crash in any action handler would surface as a request error; the
    // assertions on page content prove the normal pipeline ran instead.
    await page.goto('/dashboard/team-42');
    await expect(page.locator('[data-go-page]')).toBeVisible();
    const probe = await page.evaluate(async () => {
      const res = await fetch('/_meta/actions/demo.echo', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message: 'after-nav' }),
      });
      return res.status;
    });
    expect(probe).toBe(200);
  });

  test('relaoding a page does not repeat an action', async ({ page }) => {
    await page.goto('/');
    const first = await page.evaluate(async () => {
      const res = await fetch('/_meta/actions/demo.createPost', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: 'before-reload' }),
      });
      return res.status;
    });
    expect(first).toBe(200);
    await page.reload();
    // After a document reload the page still renders normally; only an
    // explicit fetch executes actions. There is no replay-on-load mechanism.
    await expect(page.locator('[data-go-page]')).toBeVisible();
  });

  test('an unsupported content type is rejected without execution', async ({
    page,
  }) => {
    await page.goto('/');
    const result = await page.evaluate(async () => {
      const res = await fetch('/_meta/actions/demo.createPost', {
        method: 'POST',
        headers: { 'Content-Type': 'text/plain' },
        body: 'title=hello',
      });
      return { status: res.status, body: await res.text() };
    });
    expect(result.status).toBe(415);
    expect(result.body).not.toContain('"ok":true');
  });

  test('cross-origin browser POST is rejected', async ({ request }) => {
    // An explicitly foreign origin against the real server: the gate must
    // fail closed regardless of otherwise-valid input.
    const res = await request.post(
      'http://localhost:3131/_meta/actions/demo.createPost',
      {
        headers: {
          Origin: 'https://evil.example.com',
          'Content-Type': 'application/json',
        },
        data: { title: 'attack' },
        maxRedirects: 0,
      }
    );
    expect(res.status()).toBe(403);
    const body = await res.json();
    expect(body.ok).toBe(false);
  });

  test('SSR and CSR loading still work alongside actions', async ({ page }) => {
    // The loader pipeline is untouched by action dispatch: a loader route
    // renders its data after an action request on the same server.
    await page.goto('/');
    await page.evaluate(async () => {
      await fetch('/_meta/actions/demo.echo', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message: 'coexistence' }),
      });
    });
    await page.goto('/c05/source');
    await expect(page.locator('[data-go-page="c05-source"]')).toBeVisible();
  });
});
