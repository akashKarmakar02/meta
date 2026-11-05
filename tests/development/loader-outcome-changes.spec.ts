// C06 development-suite additions (P2-C06 §19): outcome changes on an
// already-registered loader — success -> error and error -> redirect — must
// rebuild cleanly through `meta dev`, keep the proxy up, and produce the
// mapped response at the actual HTTP boundary (not just the dev proxy's
// error page). Complements ssr-loader-execution / csr-loader-execution,
// which cover edits that keep succeeding and compile failures.
import { test, expect } from '@playwright/test';
import { read, write, newDevSession, type DevSession } from './fixtures/dev-session';

let session: DevSession;

test.beforeAll(async () => {
  session = await newDevSession();
  await session.start();
});

test.afterAll(async () => {
  await session.stop();
  session.dispose();
});

// status fetches with redirects disabled; Playwright's APIRequestContext
// throws when a redirect arrives despite maxRedirects: 0 on some builds, so
// catch and surface the redirect's status/Location from the error's response.
// Replace ONLY the final success return (the multi-line literal). A naive
// /return PostData\{[\s\S]*?\}, nil/ would start matching at the earlier
// `return PostData{}, err` error return and swallow the function body,
// producing a Go compile error that leaves the dev proxy on 503. The
// params read must go too — with no literal left, `id` would be unused.
const toOutcome = (original: string, outcome: string): string =>
  original
    .replace(
      '"github.com/akashKarmakar02/meta/routecontext"',
      '"github.com/akashKarmakar02/meta/loader"\n\t"github.com/akashKarmakar02/meta/routecontext"'
    )
    .replace(
      /\tid, err := route\.Params\.Int64\("postId"\)\n\tif err != nil \{\n\t\treturn PostData\{\}, err\n\t\}\n\treturn PostData\{\n\t\tID:[\s\S]*?\n\t\}, nil/,
      `\tif _, err := route.Params.Int64("postId"); err != nil {\n\t\treturn PostData{}, err\n\t}\n\treturn PostData{}, ${outcome}`
    );

const status = async (
  request: {
    get(
      url: string,
      options?: any
    ): Promise<{ status(): number; headers(): Record<string, string> }>;
  },
  path: string
): Promise<{ code: number; location?: string }> => {
  try {
    const res = await request.get(`${session.base}${path}`, {
      timeout: 3000,
      maxRedirects: 0,
    });
    return { code: res.status(), location: res.headers()['location'] };
  } catch (err) {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const resp = (err as any)?.response;
    if (resp) {
      return { code: resp.status(), location: resp.headers()['location'] };
    }
    return { code: 0 };
  }
};

test('a loader changed from success to not-found maps to 404 after rebuild', async ({ request }) => {
  await session.waitUntil(request, '/posts/42', 200);
  const file = 'app/loaders/posts.go';
  const original = read(session.root, file);

  // Change the outcome, keep it compiling.
  write(session.root, file, toOutcome(original, 'loader.ErrNotFound'));
  await expect
    .poll(() => status(request, '/posts/42'), { timeout: 90_000 })
    .toMatchObject({ code: 404 });

  // And back to success, to prove recovery of the same binding.
  write(session.root, file, original);
  await expect
    .poll(() => status(request, '/posts/42'), { timeout: 90_000 })
    .toMatchObject({ code: 200 });
});

test('a loader changed from error to redirect maps to 302 + Location', async ({ request }) => {
  const file = 'app/loaders/posts.go';
  const original = read(session.root, file);

  // From a failing outcome straight to a redirect: rebuild then 302 with a
  // validated root-relative Location at the HTTP boundary.
  write(session.root, file, toOutcome(original, 'loader.Redirect("/about")'));
  await expect
    .poll(() => status(request, '/posts/42'), { timeout: 90_000 })
    .toMatchObject({ code: 302, location: '/about' });

  // A rejected redirect target must NOT become a Location header — it maps
  // to 500 instead (validation at the boundary, per C05/C06).
  write(session.root, file, toOutcome(original, 'loader.Redirect("https://evil.example.com")'));
  await expect
    .poll(() => status(request, '/posts/42'), { timeout: 90_000 })
    .toMatchObject({ code: 500 });

  write(session.root, file, original);
  await expect
    .poll(() => status(request, '/posts/42'), { timeout: 90_000 })
    .toMatchObject({ code: 200 });
});
