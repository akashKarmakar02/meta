package httpserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"go-meta/imageopt"
	"go-meta/imageproc"
	"go-meta/internal/imagecache"
)

// F05 HTTP integration tests: disk-cache behavior over real requests with
// fake processors, plus corrupted-entry and header parity checks. Real
// native encode→hit flows are covered by the tagged native-image suite.

// f05Server builds an app like f03Server plus a disk cache wired through
// ImageEndpointConfig.
func f05Server(t *testing.T, proc imageproc.Processor, cacheCfg imagecache.Config) (*fiber.App, string, *imagecache.Cache) {
	t.Helper()
	dir := t.TempDir()
	resolver, err := NewImageResolver(map[string]string{"/images/": dir}, DefaultImageSourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resolver.Close() })
	cache, err := imagecache.Open(cacheCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	app := fiber.New(fiber.Config{ReadBufferSize: 65536})
	if err := MountImageProcessing(app, imageopt.DefaultLimits(), ImageEndpointConfig{
		Resolver:      resolver,
		Processor:     proc,
		MaxConcurrent: 2,
		Cache:         cache,
	}); err != nil {
		t.Fatal(err)
	}
	app.Get("/*", func(c fiber.Ctx) error { return c.SendString("page") })
	return app, dir, cache
}

// countingPassthrough wraps passthrough to count Process invocations and
// produce format-magic-valid output, which the disk cache requires before
// publication (raw "OUT:webp" text is not valid RIFF/WEBP and would be
// rejected as corruption on every hit).
type countingPassthrough struct {
	passthrough
	calls atomic.Int64
}

func (p *countingPassthrough) Process(ctx context.Context, src imageproc.Source, tr imageproc.Transform) (imageproc.Result, error) {
	p.calls.Add(1)
	res, err := p.passthrough.Process(ctx, src, tr)
	if err != nil {
		return res, err
	}
	res.Data = withMagic(tr.Format, res.Data)
	return res, nil
}

// withMagic prefixes bytes with the concrete format's magic so cache
// publication and hit verification accept them.
func withMagic(f imageopt.Format, data []byte) []byte {
	var hdr []byte
	switch f {
	case imageopt.FormatWebP:
		hdr = []byte("RIFF0000WEBP")
	case imageopt.FormatAVIF:
		hdr = []byte{0, 0, 0, 12, 'f', 't', 'y', 'p', 'a', 'v', 'i', 'f'}
	case imageopt.FormatJPEG:
		hdr = []byte{0xFF, 0xD8}
	case imageopt.FormatPNG:
		hdr = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}
	}
	return append(append([]byte{}, hdr...), data...)
}

func f05CacheCfg(t *testing.T) imagecache.Config {
	t.Helper()
	return imagecache.Config{
		Dir:           filepath.Join(t.TempDir(), "imgcache"),
		MaxTotalBytes: 16 << 20,
		MaxEntries:    128,
		MaxEntryBytes: 4 << 20,
	}
}

func f05Body(t *testing.T, app *fiber.App, path string) (int, http.Header, []byte) {
	t.Helper()
	res, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, body
}

func TestF05MissThenHitSkipsProcessor(t *testing.T) {
	proc := &countingPassthrough{passthrough: passthrough{caps: fullCaps()}}
	app, dir, _ := f05Server(t, proc, f05CacheCfg(t))
	if err := os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("V1-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	const q = "/_meta/image?src=%2Fimages%2Fhero.jpg&w=100&f=webp"
	code, hdr, body1 := f05Body(t, app, q)
	if code != 200 || !bytes.Contains(body1, []byte("OUT:webp")) {
		t.Fatalf("miss: %d %q", code, body1)
	}
	if got := proc.calls.Load(); got != 1 {
		t.Fatalf("miss invoked processor %d times", got)
	}

	code2, hdr2, body2 := f05Body(t, app, q)
	if code2 != 200 || !bytes.Equal(body1, body2) {
		t.Fatalf("hit mismatch: %d", code2)
	}
	if got := proc.calls.Load(); got != 1 {
		t.Fatalf("HIT re-invoked processor (calls=%d) — cache did not serve", got)
	}
	_ = hdr
	_ = hdr2
}

func TestF05HeadersOnHit(t *testing.T) {
	proc := passthrough{caps: fullCaps()}
	app, dir, _ := f05Server(t, proc, f05CacheCfg(t))
	os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("V1"), 0o644)
	const q = "/_meta/image?src=%2Fimages%2Fhero.jpg&w=100&f=webp"
	f05Body(t, app, q) // miss
	code, hdr, body := f05Body(t, app, q)
	if code != 200 {
		t.Fatalf("hit status %d", code)
	}
	if hdr.Get("Content-Type") != "image/webp" {
		t.Fatalf("Content-Type %q", hdr.Get("Content-Type"))
	}
	if hdr.Get("Content-Length") != fmt.Sprint(len(body)) {
		t.Fatalf("Content-Length %q for %d bytes", hdr.Get("Content-Length"), len(body))
	}
	if hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control %q — F03's immutable header must not return", hdr.Get("Cache-Control"))
	}
}

// echoPassthrough embeds the source bytes in its output so tests can
// observe WHICH source version was served, unlike passthrough.
type echoPassthrough struct {
	passthrough
	calls atomic.Int64
}

func (p *echoPassthrough) Process(ctx context.Context, src imageproc.Source, tr imageproc.Transform) (imageproc.Result, error) {
	p.calls.Add(1)
	res, err := p.passthrough.Process(ctx, src, tr)
	if err != nil {
		return res, err
	}
	res.Data = withMagic(tr.Format, append(res.Data, src.Bytes...))
	return res, nil
}

func TestF05SameSizeSameMtimeReplacementInvalidates(t *testing.T) {
	proc := &echoPassthrough{passthrough: passthrough{caps: fullCaps()}}
	app, dir, _ := f05Server(t, proc, f05CacheCfg(t))
	src := filepath.Join(dir, "hero.jpg")
	v1 := []byte("AAAA-source") // 11 bytes
	v2 := []byte("BBBB-source") // 11 bytes, same length
	if len(v1) != len(v2) {
		t.Fatal("fixture must be same size")
	}
	os.WriteFile(src, v1, 0o644)
	st, _ := os.Stat(src)

	const q = "/_meta/image?src=%2Fimages%2Fhero.jpg&w=100&f=webp"
	_, _, first := f05Body(t, app, q)
	_, _, second := f05Body(t, app, q)
	if !bytes.Equal(first, second) {
		t.Fatal("hit bytes differ before replacement")
	}
	if calls := proc.calls.Load(); calls != 1 {
		t.Fatalf("hit after miss re-processed (%d)", calls)
	}

	// Replace with SAME SIZE and PRESERVED mtime.
	os.WriteFile(src, v2, 0o644)
	os.Chtimes(src, st.ModTime(), st.ModTime())

	code, _, third := f05Body(t, app, q)
	if code != 200 {
		t.Fatalf("post-replacement status %d", code)
	}
	if !bytes.HasSuffix(third, v2) {
		t.Fatalf("stale V1 output served after same-size/same-mtime replacement: %q", third)
	}
	if calls := proc.calls.Load(); calls != 2 {
		t.Fatalf("replacement must re-process once (%d calls)", calls)
	}
	// And the fresh entry hits again without reprocessing.
	f05Body(t, app, q)
	if calls := proc.calls.Load(); calls != 2 {
		t.Fatalf("fresh entry did not hit (%d calls)", calls)
	}
}

func TestF05FormatVariationDistinctEntries(t *testing.T) {
	proc := &countingPassthrough{passthrough: passthrough{caps: fullCaps()}}
	app, dir, _ := f05Server(t, proc, f05CacheCfg(t))
	os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("src"), 0o644)

	const webpQ = "/_meta/image?src=%2Fimages%2Fhero.jpg&w=100&f=webp"
	const avifQ = "/_meta/image?src=%2Fimages%2Fhero.jpg&w=100&f=avif"
	c1, h1, b1 := f05Body(t, app, webpQ)
	c2, _, _ := f05Body(t, app, webpQ) // webp hit
	if c1 != 200 || c2 != 200 {
		t.Fatalf("webp miss/hit: %d %d", c1, c2)
	}
	if h1.Get("Content-Type") != "image/webp" {
		t.Fatalf("webp Content-Type %q", h1.Get("Content-Type"))
	}
	ca, ha, ba := f05Body(t, app, avifQ)
	if ca != 200 || ha.Get("Content-Type") != "image/avif" || !bytes.Contains(ba, []byte("OUT:avif")) {
		t.Fatalf("avif request: %d %q %q", ca, ha.Get("Content-Type"), ba)
	}
	if bytes.Equal(b1, ba) {
		t.Fatal("webp and avif share one entry")
	}
	// WebP entry still served correctly afterwards.
	_, _, b1b := f05Body(t, app, webpQ)
	if !bytes.Equal(b1, b1b) {
		t.Fatal("webp entry disturbed by avif publication")
	}
}

func TestF05AutoNegotiationResolvesBeforeKeying(t *testing.T) {
	proc := &countingPassthrough{passthrough: passthrough{caps: fullCaps()}}
	app, dir, _ := f05Server(t, proc, f05CacheCfg(t))
	os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("src"), 0o644)
	req := func(accept string) (int, string, []byte) {
		r := httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fhero.jpg&w=100&f=auto", nil)
		if accept != "" {
			r.Header.Set("Accept", accept)
		}
		res, err := app.Test(r)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, res.Header.Get("Content-Type"), b
	}
	c1, ct1, _ := req("image/avif,image/webp,*/*;q=0.8")
	if c1 != 200 || ct1 != "image/avif" {
		t.Fatalf("auto avif: %d %s", c1, ct1)
	}
	c2, ct2, _ := req("image/avif,image/webp,*/*;q=0.8")
	if c2 != 200 || ct2 != "image/avif" {
		t.Fatalf("auto avif hit: %d %s", c2, ct2)
	}
	c3, ct3, _ := req("image/webp,image/jpeg")
	if c3 != 200 || ct3 != "image/webp" {
		t.Fatalf("auto webp after avif hit: %d %s — formats must not share entries", c3, ct3)
	}
	if calls := proc.calls.Load(); calls != 2 {
		t.Fatalf("processor ran %d times; auto-negotiated variants must each encode once", calls)
	}
}

func TestF05CorruptEntryRecovers(t *testing.T) {
	proc := &countingPassthrough{passthrough: passthrough{caps: fullCaps()}}
	dir := t.TempDir()
	cfg := imagecache.Config{Dir: filepath.Join(dir, "imgcache"), MaxTotalBytes: 16 << 20, MaxEntries: 128, MaxEntryBytes: 4 << 20}
	cache, err := imagecache.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	resolver, err := NewImageResolver(map[string]string{"/images/": dir}, DefaultImageSourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resolver.Close() })
	app := fiber.New(fiber.Config{ReadBufferSize: 65536})
	if err := MountImageProcessing(app, imageopt.DefaultLimits(), ImageEndpointConfig{
		Resolver: resolver, Processor: proc, MaxConcurrent: 2, Cache: cache,
	}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("src"), 0o644)
	const q = "/_meta/image?src=%2Fimages%2Fhero.jpg&w=100&f=webp"

	// Prime the cache with garbage behind the correct key: the passthrough
	// writes "OUT:webp..." which is NOT valid webp magic, so publication
	// fails and no entry exists. Instead simulate corruption by publishing
	// valid-magic bytes then truncating on disk.
	// Use jpeg: SOI magic is two bytes, easiest to hand-forge.
	const jq = "/_meta/image?src=%2Fimages%2Fhero.jpg&w=100&f=jpeg"
	_, _, first := f05Body(t, app, jq)
	if !bytes.Contains(first, []byte("OUT:jpeg")) {
		t.Fatalf("jpeg miss unexpected: %q", first)
	}
	// Corrupt every cached file on disk.
	var corrupted int
	for s := 0; s < 256; s++ {
		shard := fmt.Sprintf("%02x", s)
		ents, err := os.ReadDir(filepath.Join(cfg.Dir, shard))
		if err != nil {
			continue
		}
		for _, e := range ents {
			p := filepath.Join(cfg.Dir, shard, e.Name())
			os.WriteFile(p, []byte("XX-corrupted"), 0o600)
			corrupted++
		}
	}
	if corrupted == 0 {
		t.Fatal("no entries to corrupt")
	}
	before := proc.calls.Load()
	// Request again: hit verification rejects corrupt bytes, removes them,
	// reprocesses once, republishes.
	code, hdr, body := f05Body(t, app, jq)
	if code != 200 || !bytes.Contains(body, []byte("OUT:jpeg")) {
		t.Fatalf("corruption recovery failed: %d %q", code, body)
	}
	if hdr.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("mime wrong after recovery: %q", hdr.Get("Content-Type"))
	}
	if calls := proc.calls.Load(); calls != before+1 {
		t.Fatalf("recovery processed %d times (want +1)", calls-before)
	}
}

func TestF05CacheDisabledIsNoop(t *testing.T) {
	// No Cache in config: behavior identical to pre-F05, no directory use.
	proc := &countingPassthrough{passthrough: passthrough{caps: fullCaps()}}
	dir := t.TempDir()
	resolver, err := NewImageResolver(map[string]string{"/images/": dir}, DefaultImageSourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resolver.Close() })
	app := fiber.New(fiber.Config{ReadBufferSize: 65536})
	if err := MountImageProcessing(app, imageopt.DefaultLimits(), ImageEndpointConfig{
		Resolver: resolver, Processor: proc, MaxConcurrent: 2,
	}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("src"), 0o644)
	q := "/_meta/image?src=%2Fimages%2Fhero.jpg&w=100&f=webp"
	f05Body(t, app, q)
	f05Body(t, app, q)
	if calls := proc.calls.Load(); calls != 2 {
		t.Fatalf("without a cache every request processes (calls=%d)", calls)
	}
}

func TestF05CachePutFailureStillServes(t *testing.T) {
	// Processor failure on the SECOND request would be masked if we tried
	// to hide it; instead prove the complement: cache write failure must
	// not fail an otherwise-successful response. Simulate by closing the
	// cache between the miss and republish path: Put then returns
	// ErrCacheClosed, response still succeeds.
	proc := &countingPassthrough{passthrough: passthrough{caps: fullCaps()}}
	app, dir, cache := f05Server(t, proc, f05CacheCfg(t))
	os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("src"), 0o644)
	q := "/_meta/image?src=%2Fimages%2Fhero.jpg&w=100&f=jpeg"
	if code, _, _ := f05Body(t, app, q); code != 200 {
		t.Fatalf("first request failed")
	}
	// Wipe the entry on disk AND close the cache: next request gets no
	// hit, processes, and its Put fails with ErrCacheClosed — yet must
	// still answer 200.
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	code, hdr, body := f05Body(t, app, q)
	if code != 200 || !bytes.Contains(body, []byte("OUT:jpeg")) {
		t.Fatalf("closed-cache publish must not fail response: %d %q", code, body)
	}
	if hdr.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("mime %q", hdr.Get("Content-Type"))
	}
}

func TestF05HeadHitsWithoutProcessing(t *testing.T) {
	proc := &countingPassthrough{passthrough: passthrough{caps: fullCaps()}}
	app, dir, _ := f05Server(t, proc, f05CacheCfg(t))
	os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("src"), 0o644)
	const q = "/_meta/image?src=%2Fimages%2Fhero.jpg&w=100&f=webp"
	// Prime.
	if code, _, _ := f05Body(t, app, q); code != 200 {
		t.Fatal("prime failed")
	}
	before := proc.calls.Load()

	req := httptest.NewRequest(http.MethodHead, q, nil)
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		t.Fatalf("HEAD hit status %d", res.StatusCode)
	}
	if len(body) != 0 {
		t.Fatalf("HEAD returned %d bytes", len(body))
	}
	if res.Header.Get("Content-Type") != "image/webp" {
		t.Fatalf("HEAD hit mime %q", res.Header.Get("Content-Type"))
	}
	if res.Header.Get("Content-Length") == "" {
		t.Fatal("HEAD hit missing Content-Length")
	}
	if calls := proc.calls.Load(); calls != before {
		t.Fatalf("HEAD hit invoked processor (%d new calls)", calls-before)
	}
}

func TestF05RemovedSourceNotServedFromCache(t *testing.T) {
	proc := &countingPassthrough{passthrough: passthrough{caps: fullCaps()}}
	app, dir, _ := f05Server(t, proc, f05CacheCfg(t))
	src := filepath.Join(dir, "hero.jpg")
	os.WriteFile(src, []byte("src"), 0o644)
	q := "/_meta/image?src=%2Fimages%2Fhero.jpg&w=100&f=webp"
	if code, _, _ := f05Body(t, app, q); code != 200 {
		t.Fatal("prime failed")
	}
	os.Remove(src)
	code, hdr, body := f05Body(t, app, q)
	if code != 404 {
		t.Fatalf("removed source served from cache: %d %q", code, body)
	}
	if hdr.Get("Cache-Control") != "no-store" {
		t.Fatal("404 without no-store")
	}
}

func TestF05ProcessorVersionChangesIdentity(t *testing.T) {
	// Two processors differing only in Version() must produce distinct
	// cache entries for the same transform.
	v1 := &versionedFake{version: "proc-a"}
	v2 := &versionedFake{version: "proc-b"}
	cacheCfg := f05CacheCfg(t)

	app1, dir1, cache := f05Server(t, v1, cacheCfg)
	_ = cache
	os.WriteFile(filepath.Join(dir1, "hero.jpg"), []byte("shared-src"), 0o644)
	q := "/_meta/image?src=%2Fimages%2Fhero.jpg&w=100&f=webp"
	_, _, b1 := f05Body(t, app1, q)
	_, _, b1h := f05Body(t, app1, q)
	if !bytes.Equal(b1, b1h) {
		t.Fatal("same-version hit mismatch")
	}

	// Reopen the SAME cache directory with processor B: different version
	// → different identity → must process (miss), not serve A's bytes.
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "hero.jpg"), []byte("shared-src"), 0o644)
	resolver, err := NewImageResolver(map[string]string{"/images/": dir2}, DefaultImageSourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resolver.Close() })
	cache2, err := imagecache.Open(cacheCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cache2.Close()
	app2 := fiber.New(fiber.Config{ReadBufferSize: 65536})
	if err := MountImageProcessing(app2, imageopt.DefaultLimits(), ImageEndpointConfig{
		Resolver: resolver, Processor: v2, MaxConcurrent: 2, Cache: cache2,
	}); err != nil {
		t.Fatal(err)
	}
	_, _, b2 := f05Body(t, app2, q)
	if bytes.Equal(b1, b2) {
		t.Fatal("version B consumed version A's entry")
	}
	if v2.calls != 1 {
		t.Fatalf("version B processed %d times; distinct versions must miss", v2.calls)
	}
}

// versionedFake implements Processor + Versioned with per-version output.
type versionedFake struct {
	version string
	calls   int
}

func (v *versionedFake) Capabilities() imageproc.Capabilities {
	return imageproc.Capabilities{JPEGDecode: true, PNGDecode: true, JPEGEncode: true, PNGEncode: true, WebPEncode: true, AVIFEncode: true}
}

func (v *versionedFake) Version() string { return v.version }

func (v *versionedFake) Process(_ context.Context, src imageproc.Source, tr imageproc.Transform) (imageproc.Result, error) {
	v.calls++
	mime, _ := tr.Format.MIME()
	data := append([]byte("VER:"+v.version+":"), []byte(string(tr.Format))...)
	// Give each format valid magic so publication accepts it.
	switch tr.Format {
	case imageopt.FormatJPEG:
		data = append([]byte{0xFF, 0xD8}, data...)
	case imageopt.FormatPNG:
		hdr := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}
		data = append(hdr, data...)
	default:
		// webp/avif magic variants for the fake.
		if tr.Format == imageopt.FormatWebP {
			data = append([]byte("RIFF0000WEBP"), data...)
		} else {
			b := make([]byte, 12, 12+len(data))
			copy(b[4:], "ftyp")
			copy(b[8:], "avif")
			data = append(b, data...)
		}
	}
	w, h := tr.Width, tr.Height
	if w == 0 {
		w = h
	}
	if h == 0 {
		h = w
	}
	return imageproc.Result{Data: data, Format: tr.Format, MIME: mime, Width: w, Height: h, Version: v.version}, nil
}

func TestF05ConcurrentSameKeyRequestsSafe(t *testing.T) {
	proc := &countingPassthrough{passthrough: passthrough{caps: fullCaps()}}
	app, dir, _ := f05Server(t, proc, f05CacheCfg(t))
	os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("src"), 0o644)
	q := "/_meta/image?src=%2Fimages%2Fhero.jpg&w=100&f=webp"

	const n = 12
	results := make([][]byte, n)
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			code, _, body := f05Body(t, app, q)
			codes[i] = code
			results[i] = body
		}(i)
	}
	wg.Wait()
	for i := range n {
		if codes[i] != 200 {
			t.Fatalf("request %d: %d", i, codes[i])
		}
		if !bytes.Equal(results[i], results[0]) {
			t.Fatalf("request %d bytes differ under concurrency", i)
		}
	}
}

func TestF05ErrorsCarryNoCacheDetails(t *testing.T) {
	proc := passthrough{caps: fullCaps()}
	app, dir, _ := f05Server(t, proc, f05CacheCfg(t))
	os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("src"), 0o644)
	for _, q := range []string{
		"/_meta/image?src=%2Fimages%2Fmissing.jpg&w=100&f=webp",
		"/_meta/image?src=x&w=100&f=webp",
		"/_meta/image?src=%2Fimages%2Fhero.jpg&w=100&f=svg",
	} {
		code, _, body := f05Body(t, app, q)
		if code < 400 {
			t.Fatalf("%s: %d", q, code)
		}
		low := strings.ToLower(string(body))
		for _, leak := range []string{"imgcache", "imagecache", filepath.Join("tmp", ""), "no such file", "/home/", "rename"} {
			if strings.Contains(low, leak) {
				t.Fatalf("%s leaked %q: %s", q, leak, body)
			}
		}
	}
}

// §38: source replacement DURING an active transformation. A reads V1 and
// begins slow processing; the file is replaced with V2; B then processes.
// A's entry must be keyed to V1 only, B's to V2, and neither response may
// mix source bytes.
func TestF05SourceChangeDuringProcessing(t *testing.T) {
	slow := &slowEcho{passthrough: passthrough{caps: fullCaps()}}
	app, dir, _ := f05Server(t, slow, f05CacheCfg(t))
	src := filepath.Join(dir, "hero.jpg")
	v1 := []byte("V1-payload")
	v2 := []byte("V2-payload")
	os.WriteFile(src, v1, 0o644)
	q := "/_meta/image?src=%2Fimages%2Fhero.jpg&w=100&f=webp"

	// A: starts, blocks inside Process after reading its snapshot.
	aStarted := make(chan struct{})
	releaseA := make(chan struct{})
	slow.block = func() { close(aStarted); <-releaseA }
	resA := make(chan []byte, 1)
	go func() {
		res, err := app.Test(httptest.NewRequest(http.MethodGet, q, nil))
		if err != nil {
			t.Error(err)
		}
		body, _ := io.ReadAll(res.Body)
		resA <- body
	}()
	<-aStarted
	slow.block = nil // only A's Process call blocks

	// Replace the source while A holds its V1 snapshot.
	os.WriteFile(src, v2, 0o644)
	close(releaseA) // A completes on V1 bytes

	var gotA []byte
	select {
	case gotA = <-resA:
	case <-time.After(5 * time.Second):
		t.Fatal("A never completed")
	}
	if !bytes.HasSuffix(gotA, v1) {
		t.Fatalf("A served wrong/new source: %q", gotA)
	}

	// B: processes V2 under a different identity.
	resB, err := app.Test(httptest.NewRequest(http.MethodGet, q, nil))
	if err != nil {
		t.Fatal(err)
	}
	gotB, _ := io.ReadAll(resB.Body)
	if !bytes.HasSuffix(gotB, v2) {
		t.Fatalf("B served stale source: %q", gotB)
	}
}

// slowEcho embeds source bytes in output (like echoPassthrough) and can
// block mid-Process for deterministic race construction.
type slowEcho struct {
	passthrough
	block func()
}

func (s *slowEcho) Process(ctx context.Context, src imageproc.Source, tr imageproc.Transform) (imageproc.Result, error) {
	res, err := s.passthrough.Process(ctx, src, tr)
	if err != nil {
		return res, err
	}
	res.Data = withMagic(tr.Format, append(res.Data, src.Bytes...))
	if s.block != nil {
		s.block()
	}
	return res, nil
}
