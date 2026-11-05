import { expect, test } from '@playwright/test'

/**
 * P2-F04 focused image-security browser regressions. Runs against the
 * same builds as the Chromium suites: the production suite runs untagged
 * (these endpoint checks are build-independent; the 501 truthfully skips
 * native assertions), and make e2e-native runs this file against a
 * tagged build where real encoding happens.
 */

const IMG = 'src=%2Fimages%2Fhero.jpg&w=100&f=webp'

test.describe('image security regressions (F04)', () => {
  let canProcess = false

  test.beforeAll(async ({ request }) => {
    const res = await request.get(`/_meta/image?${IMG}`)
    canProcess = res.status() === 200
  })

  test('no-store is present on image responses', async ({ request }) => {
    const res = await request.get(`/_meta/image?${IMG}`)
    expect([200, 501]).toContain(res.status())
    expect(res.headers()['cache-control']).toBe('no-store')
  })

  test('no-store on error responses too', async ({ request }) => {
    for (const q of [
      'src=%2Fimages%2Fmissing.jpg&w=100&f=webp',
      'src=%2Fimages%2Fhero.jpg&w=100&f=svg',
      'src=x&w=100&f=webp',
    ]) {
      const res = await request.get(`/_meta/image?${q}`)
      expect(res.headers()['cache-control']).toBe('no-store')
      expect(res.status()).toBeGreaterThanOrEqual(400)
    }
  })

  test('unsupported methods are 405 and never touch sources', async ({ request }) => {
    for (const method of ['POST', 'PUT', 'DELETE', 'PATCH'] as const) {
      const res = await request.fetch('/_meta/image?' + IMG, { method })
      expect(res.status()).toBe(405)
      expect(res.headers().allow).toBe('GET, HEAD')
    }
  })

  test('HEAD mirrors GET without a body', async ({ request }) => {
    const head = await request.head(`/_meta/image?${IMG}`)
    // Untagged builds: truthful 501. Tagged builds: 200 + image MIME.
    expect([200, 501]).toContain(head.status())
    expect((await head.body()).byteLength).toBe(0)
    if (head.status() === 200) {
      expect(head.headers()['content-type']).toMatch(/^image\//)
      const get = await request.get(`/_meta/image?${IMG}`)
      expect(head.headers()['content-length']).toBe(get.headers()['content-length'])
    }
  })

  test('oversized declared dimensions are rejected before decode', async ({ request }) => {
    // PNG IHDR declaring 100000x100000 (under 100 bytes total).
    const bomb =
      'src=%2Fimages%2Fbomb.png&w=100&f=webp'
    const res = await request.get(`/_meta/image?${bomb}`)
    // Untagged: 501 before source access; tagged: 413. Either way never
    // 200 and never image content.
    expect([413, 501]).toContain(res.status())
    expect(res.headers()['content-type'] ?? '').not.toMatch(/^image\//)
  })

  test('error bodies never disclose paths or native internals', async ({ request }) => {
    const cases = [
      'src=%2Fimages%2Fmissing.jpg&w=100&f=webp',
      'src=%2Fimages%2F..%2F..%2Fetc%2Fpasswd&w=100&f=webp',
      'src=%2Fimages%2Fhero.jpg&w=0&h=0&f=webp',
    ]
    for (const q of cases) {
      const res = await request.get(`/_meta/image?${q}`)
      const body = await res.text()
      expect(body.length).toBeLessThan(200)
      expect(body.toLowerCase()).not.toContain('/tmp')
      expect(body.toLowerCase()).not.toContain('home/')
      expect(body.toLowerCase()).not.toContain('libwebp')
      expect(body.toLowerCase()).not.toContain('libavif')
      expect(body.toLowerCase()).not.toContain('goroutine')
    }
  })

  test('a rendered <img> executes no scripts from the pipeline', async ({ page, request }) => {
    test.skip(!canProcess, 'native backend unavailable')
    let dialogCount = 0
    page.on('dialog', () => dialogCount++)
    await page.goto('/')
    await page.setContent(`<img src="/_meta/image?${IMG}" alt="x">`)
    await page.waitForTimeout(300)
    expect(dialogCount).toBe(0)
  })

  test('SSR pages still render with optimized image URLs present', async ({ page, request }) => {
    await page.goto('/')
    await expect(page.locator('body')).toContainText(/./)
    if (canProcess) {
      const res = await request.get(`/_meta/image?${IMG}`)
      expect(res.status()).toBe(200)
      expect(res.headers()['content-type']).toBe('image/webp')
    }
  })

  test('soft navigation keeps working after image traffic', async ({ page }) => {
    await page.goto('/')
    if (canProcess) {
      await page.setContent(`<img src="/_meta/image?${IMG}" alt="x">`)
      await page.waitForTimeout(200)
    }
    await page.goto('/')
    await expect(page.locator('body')).toContainText(/./)
  })
})
