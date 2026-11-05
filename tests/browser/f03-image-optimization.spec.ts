import { expect, test } from '@playwright/test'

/**
 * P2-F03 focused native-image tests. This spec runs against a build of
 * cmd/example compiled with -tags imageproc_native; when the runtime
 * libraries are absent the endpoints stay at 501 and these tests skip.
 * The untagged contract is covered by f01-image-contract.spec.ts.
 */

const IMAGE_PARAMS = 'src=%2Fimages%2Fhero.jpg&w=800&f='

test.describe('image optimization (native build)', () => {
  let canProcess = false

  test.beforeAll(async ({ request }) => {
    const res = await request.get(`/_meta/image?${IMAGE_PARAMS}webp`)
    canProcess = res.status() === 200
  })

  test('optimized WebP image displays with correct natural dimensions', async ({ page, request }) => {
    test.skip(!canProcess, 'native image backend unavailable in this run')
    // Verify the response first: real bytes, correct MIME.
    const res = await request.get(`/_meta/image?${IMAGE_PARAMS}webp`)
    expect(res.status()).toBe(200)
    expect(res.headers()['content-type']).toBe('image/webp')
    const buf = await res.body()
    expect(buf.byteLength).toBeGreaterThan(100)

    // Then verify the browser actually renders it via an <img>. Navigate
    // first so the relative src resolves against the app's origin.
    await page.goto('/')
    await page.setContent(
      `<img id="hero" src="/_meta/image?${IMAGE_PARAMS}webp" alt="hero">`,
    )
    await expect
      .poll(async () => page.evaluate(() => (document.getElementById('hero') as HTMLImageElement).naturalWidth))
      .toBe(800)
    await expect
      .poll(async () => page.evaluate(() => (document.getElementById('hero') as HTMLImageElement).naturalHeight))
      .toBe(450)
    await expect
      .poll(async () => page.evaluate(() => (document.getElementById('hero') as HTMLImageElement).complete))
      .toBe(true)
  })

  test('optimized AVIF image decodes where the browser supports it', async ({ page, request }) => {
    test.skip(!canProcess, 'native image backend unavailable in this run')
    const supportsAvif = await page.evaluate(
      () =>
        new Promise<boolean>((resolve) => {
          const img = new Image()
          img.onload = () => resolve(img.width > 0)
          img.onerror = () => resolve(false)
          img.src =
            'data:image/avif;base64,AAAAIGZ0eXBhdmlmAAAAAGF2yWZtaWYybgu7bWlm1k3XkGJhYWlmOT'
        }),
    )
    const res = await request.get(`/_meta/image?${IMAGE_PARAMS}avif`)
    expect(res.status()).toBe(200)
    expect(res.headers()['content-type']).toBe('image/avif')

    if (!supportsAvif) {
      test.skip(true, 'this Chromium does not decode AVIF')
      return
    }
    await page.setContent(
      `<img id="hero" src="/_meta/image?${IMAGE_PARAMS}avif" alt="hero">`,
    )
    await expect
      .poll(async () => page.evaluate(() => (document.getElementById('hero') as HTMLImageElement).naturalWidth))
      .toBe(800)
  })

  test('auto negotiation picks a supported format and honors Accept weights', async ({ request }) => {
    test.skip(!canProcess, 'native image backend unavailable in this run')
    const avifOnly = await request.get(`/_meta/image?${IMAGE_PARAMS}auto`, {
      headers: { accept: 'image/avif,image/webp;q=0.5' },
    })
    expect(avifOnly.status()).toBe(200)
    expect(avifOnly.headers()['content-type']).toBe('image/avif')
    expect(avifOnly.headers()['vary']).toBe('Accept')

    const webpOnly = await request.get(`/_meta/image?${IMAGE_PARAMS}auto`, {
      headers: { accept: 'image/webp,image/avif;q=0' },
    })
    expect(webpOnly.status()).toBe(200)
    expect(webpOnly.headers()['content-type']).toBe('image/webp')

    const jpegOnly = await request.get(`/_meta/image?${IMAGE_PARAMS}auto`, {
      headers: { accept: 'image/jpeg' },
    })
    expect(jpegOnly.status()).toBe(200)
    expect(jpegOnly.headers()['content-type']).toBe('image/jpeg')
  })

  test('missing source returns 404, traversal rejected before resolution', async ({ request }) => {
    test.skip(!canProcess, 'native image backend unavailable in this run')
    const missing = await request.get('/_meta/image?src=%2Fimages%2Fnope.jpg&w=100&f=webp')
    expect(missing.status()).toBe(404)
    const body = await missing.text()
    expect(body).not.toContain('..')
    expect(body.toLowerCase()).not.toContain('/tmp')
    expect(body.toLowerCase()).not.toContain('home/')

    // Traversal is rejected by F01 parsing (400), never reaches resolver.
    const traversal = await request.get('/_meta/image?src=%2Fimages%2F..%2F..%2Fetc%2Fpasswd&w=100&f=webp')
    expect(traversal.status()).toBe(400)
  })

  test('existing loader cache stays intact when requesting images', async ({ page, request }) => {
    test.skip(!canProcess, 'native image backend unavailable in this run')
    // An image fetch must not execute the navigation envelope or loader.
    const res = await request.get(`/_meta/image?${IMAGE_PARAMS}webp`, {
      headers: { 'x-go-web-navigation': '1' },
    })
    expect(res.status()).toBe(200)
    const ct = res.headers()['content-type']
    expect(ct).toBe('image/webp')

    // The example pages still navigate normally after image activity.
    await page.goto('/')
    await expect(page.locator('body')).toContainText(/./)
  })
})
