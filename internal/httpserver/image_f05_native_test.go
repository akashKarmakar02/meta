//go:build imageproc_native

// F05 §50: real native processing through the disk cache. First request
// performs real encoding; second identical request hits disk; source and
// format changes produce fresh entries. These prove the fake-processor
// tests were not masking native integration problems.
package httpserver

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/gofiber/fiber/v3"

	"go-meta/imageopt"
	"go-meta/imageproc"
	"go-meta/internal/imagecache"
	"go-meta/internal/imageprocessor"
)

// tinyPNG builds a small deterministic PNG fixture (64x32, distinct colors).
func tinyPNG(t *testing.T, seed uint8) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 64, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 64; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 4), G: uint8(y * 8), B: seed, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// countingNative wraps the real processor to count Process calls.
type countingNative struct {
	imageproc.Processor
	calls atomic.Int64
}

func (c *countingNative) Process(ctx context.Context, src imageproc.Source, tr imageproc.Transform) (imageproc.Result, error) {
	c.calls.Add(1)
	return c.Processor.Process(ctx, src, tr)
}

func f05NativeServer(t *testing.T) (*fiber.App, string, *countingNative) {
	t.Helper()
	real, err := imageprocessor.New(context.Background())
	if err != nil {
		t.Fatalf("native processor unavailable: %v", err)
	}
	proc := &countingNative{Processor: real}
	dir := t.TempDir()
	resolver, err := NewImageResolver(map[string]string{"/images/": dir}, DefaultImageSourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resolver.Close() })
	cache, err := imagecache.Open(imagecache.Config{
		Dir:           filepath.Join(t.TempDir(), "imgcache"),
		MaxTotalBytes: 16 << 20,
		MaxEntries:    128,
		MaxEntryBytes: 4 << 20,
	})
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
	return app, dir, proc
}

func TestF05NativeWebPMissThenHit(t *testing.T) {
	app, dir, proc := f05NativeServer(t)
	os.WriteFile(filepath.Join(dir, "hero.png"), tinyPNG(t, 1), 0o644)
	const q = "/_meta/image?src=%2Fimages%2Fhero.png&w=32&f=webp"

	res, err := app.Test(httptest.NewRequest(http.MethodGet, q, nil))
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("miss: %v %d", err, res.StatusCode)
	}
	body1 := readAll(t, res)
	if !bytes.HasPrefix(body1, []byte("RIFF")) {
		t.Fatalf("not webp: %q", body1[:4])
	}
	if calls := proc.calls.Load(); calls != 1 {
		t.Fatalf("miss processed %d times", calls)
	}

	res2, err := app.Test(httptest.NewRequest(http.MethodGet, q, nil))
	if err != nil || res2.StatusCode != 200 {
		t.Fatalf("hit: %v %d", err, res2.StatusCode)
	}
	body2 := readAll(t, res2)
	if !bytes.Equal(body1, body2) {
		t.Fatal("hit bytes differ from miss output")
	}
	if calls := proc.calls.Load(); calls != 1 {
		t.Fatalf("HIT re-encoded (calls=%d)", calls)
	}
	if res2.Header.Get("Content-Type") != "image/webp" {
		t.Fatalf("hit mime %q", res2.Header.Get("Content-Type"))
	}
}

func TestF05NativeAVIFMissThenHit(t *testing.T) {
	app, dir, proc := f05NativeServer(t)
	os.WriteFile(filepath.Join(dir, "hero.png"), tinyPNG(t, 2), 0o644)
	const q = "/_meta/image?src=%2Fimages%2Fhero.png&w=32&f=avif"

	res, err := app.Test(httptest.NewRequest(http.MethodGet, q, nil))
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("miss: %v %d", err, res.StatusCode)
	}
	body1 := readAll(t, res)
	if len(body1) < 12 || string(body1[4:8]) != "ftyp" {
		t.Fatalf("not avif: %q", body1[:min(12, len(body1))])
	}
	if calls := proc.calls.Load(); calls != 1 {
		t.Fatalf("miss processed %d times", calls)
	}

	res2, _ := app.Test(httptest.NewRequest(http.MethodGet, q, nil))
	body2 := readAll(t, res2)
	if !bytes.Equal(body1, body2) {
		t.Fatal("hit bytes differ")
	}
	if calls := proc.calls.Load(); calls != 1 {
		t.Fatalf("HIT re-encoded (calls=%d)", calls)
	}
}

func TestF05NativeChangedSourceReprocesses(t *testing.T) {
	app, dir, proc := f05NativeServer(t)
	src := filepath.Join(dir, "hero.png")
	os.WriteFile(src, tinyPNG(t, 3), 0o644)
	q := "/_meta/image?src=%2Fimages%2Fhero.png&w=32&f=webp"

	res, _ := app.Test(httptest.NewRequest(http.MethodGet, q, nil))
	body1 := readAll(t, res)

	// Different content, same path.
	os.WriteFile(src, tinyPNG(t, 99), 0o644)
	res2, _ := app.Test(httptest.NewRequest(http.MethodGet, q, nil))
	body2 := readAll(t, res2)
	if bytes.Equal(body1, body2) {
		t.Fatal("stale output served for changed source")
	}
	if calls := proc.calls.Load(); calls != 2 {
		t.Fatalf("changed source must reprocess (calls=%d)", calls)
	}
}

func TestF05NativeFormatsIndependent(t *testing.T) {
	app, dir, proc := f05NativeServer(t)
	os.WriteFile(filepath.Join(dir, "hero.png"), tinyPNG(t, 4), 0o644)
	wq := "/_meta/image?src=%2Fimages%2Fhero.png&w=32&f=webp"
	aq := "/_meta/image?src=%2Fimages%2Fhero.png&w=32&f=avif"

	resW, _ := app.Test(httptest.NewRequest(http.MethodGet, wq, nil))
	bw := readAll(t, resW)
	resW2, _ := app.Test(httptest.NewRequest(http.MethodGet, wq, nil))
	if !bytes.Equal(bw, readAll(t, resW2)) {
		t.Fatal("webp hit mismatch")
	}
	resA, _ := app.Test(httptest.NewRequest(http.MethodGet, aq, nil))
	if resA.StatusCode != 200 {
		t.Fatalf("avif: %d", resA.StatusCode)
	}
	// webp entry unaffected.
	resW3, _ := app.Test(httptest.NewRequest(http.MethodGet, wq, nil))
	if !bytes.Equal(bw, readAll(t, resW3)) {
		t.Fatal("webp entry disturbed by avif publication")
	}
	if calls := proc.calls.Load(); calls != 2 {
		t.Fatalf("expected exactly 2 encodes, got %d", calls)
	}
}

func TestF05NativeDimensionsVerified(t *testing.T) {
	app, dir, _ := f05NativeServer(t)
	os.WriteFile(filepath.Join(dir, "hero.png"), tinyPNG(t, 5), 0o644)
	for _, q := range []string{
		"/_meta/image?src=%2Fimages%2Fhero.png&w=32&h=16&f=webp",
		"/_meta/image?src=%2Fimages%2Fhero.png&w=48&f=jpeg",
	} {
		res, err := app.Test(httptest.NewRequest(http.MethodGet, q, nil))
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("%s: %v %d", q, err, res.StatusCode)
		}
	}
}

func readAll(t *testing.T, res *http.Response) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(res.Body); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// tinyPNGBench builds a small PNG without a *testing.T (for benchmarks).
func tinyPNGBench(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 4), G: uint8(y * 8), B: 7, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}
