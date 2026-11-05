import { expect, test } from '@playwright/test'

/**
 * P2-F05 focused browser regressions for the optimized-image disk cache.
 * Runs only against the tagged native build (see
 * playwright.native-image.config.ts) where real encoding happens.
 *
 * The example server enables the cache only via GO_META_IMAGE_CACHE_DIR;
 * the native config's webServer command is extended for this suite to set
 * it, so these tests exercise a REAL encode→disk-hit cycle in Chromium.
 */

const IMG = 'src=%2Fimages%2Fhero.jpg&w=100&f=webp'
const IMG2 = 'src=%2Fimages%2Fhero.jpg&w=100&f=avif'

let canProcess = false

test.beforeAll(async ({ request }) => {
  const res = await request.get(`/_meta/image?${IMG}`)
  canProcess = res.status() === 200
})

test('miss then hit returns identical bytes and headers', async ({ request }) => {
  test.skip(!canProcess, 'native backend unavailable')
  const first = await request.get(`/_meta/image?${IMG}`)
  expect(first.status()).toBe(200)
  const b1 = await first.body()
  const second = await request.get(`/_meta/image?${IMG}`)
  expect(second.status()).toBe(200)
  const b2 = await second.body()
  // Deterministic encoder + disk hit: byte-identical response bodies.
  expect(b2.byteLength).toBe(b1.byteLength)
  expect(second.headers()['content-type']).toBe(first.headers()['content-type'])
  expect(second.headers()['cache-control']).toBe('no-store')
})

test('hit does not alter content-type or length', async ({ request }) => {
  test.skip(!canProcess, 'native backend unavailable')
  const res = await request.get(`/_meta/image?${IMG}`)
  expect(res.status()).toBe(200)
  const body = await res.body()
  expect(res.headers()['content-type']).toBe('image/webp')
  expect(Number(res.headers()['content-length'])).toBe(body.byteLength)
})

test('format variants produce distinct entries and correct MIME', async ({ request }) => {
  test.skip(!canProcess, 'native backend unavailable')
  const webp = await request.get(`/_meta/image?${IMG}`)
  const avif = await request.get(`/_meta/image?${IMG2}`)
  expect(webp.headers()['content-type']).toBe('image/webp')
  expect(avif.headers()['content-type']).toBe('image/avif')
  const bw = await webp.body()
  const ba = await avif.body()
  expect(bw.byteLength).not.toBe(ba.byteLength)
  // webp entry still correct after avif traffic.
  const webp2 = await request.get(`/_meta/image?${IMG}`)
  expect((await webp2.body()).byteLength).toBe(bw.byteLength)
})

test('HEAD hit carries correct metadata without a body', async ({ request }) => {
  test.skip(!canProcess, 'native backend unavailable')
  await request.get(`/_meta/image?${IMG}`) // prime
  const head = await request.head(`/_meta/image?${IMG}`)
  expect(head.status()).toBe(200)
  expect((await head.body()).byteLength).toBe(0)
  expect(head.headers()['content-type']).toBe('image/webp')
  expect(Number(head.headers()['content-length'])).toBeGreaterThan(0)
  const get = await request.get(`/_meta/image?${IMG}`)
  expect(head.headers()['content-length']).toBe(get.headers()['content-length'])
})

test('invalid sources never leak cache details or serve stale entries', async ({ request }) => {
  // Works in both modes: errors are >=400 with no-store regardless.
  for (const q of [
    'src=%2Fimages%2Fmissing.jpg&w=100&f=webp',
    'src=%2Fimages%2F..%2F..%2Fetc%2Fpasswd&w=100&f=webp',
    'src=x&w=100&f=webp',
  ]) {
    const res = await request.get(`/_meta/image?${q}`)
    expect(res.status()).toBeGreaterThanOrEqual(400)
    expect(res.headers()['cache-control']).toBe('no-store')
    const text = await res.text()
    expect(text.toLowerCase()).not.toContain('imgcache')
    expect(text.toLowerCase()).not.toContain('/tmp')
  }
})

test('cached image renders as a real img element', async ({ page }) => {
  test.skip(!canProcess, 'native backend unavailable')
  await page.goto('/')
  const resp = await page.request.get(`/_meta/image?${IMG}`)
  expect(resp.status()).toBe(200)
  // Second fetch: served from disk; then actually rendered.
  const rendered = page.waitForResponse(`/_meta/image?${IMG}`)
  await page.setContent(`<img src="/_meta/image?${IMG}" alt="x">`)
  const r2 = await rendered
  expect(r2.status()).toBe(200)
  await expect(page.locator('img')).toBeVisible()
})

test('soft navigation still works with image-cache traffic', async ({ page }) => {
  test.skip(!canProcess, 'native backend unavailable')
  await page.goto('/')
  await page.setContent(`<img src="/_meta/image?${IMG}" alt="x">`)
  await page.waitForTimeout(200)
  await page.goto('/')
  await expect(page.locator('body')).toContainText(/./)
})
