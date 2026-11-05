//go:build imageproc_native

// F05 §55: basic timing benchmarks of cache miss (real encode) versus hit.
// F06 owns comprehensive performance gates; these are directional numbers
// for the F05 report only.
package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gofiber/fiber/v3"

	"go-meta/imageopt"
	"go-meta/internal/imagecache"
	"go-meta/internal/imageprocessor"
)

func benchServer(b *testing.B) (*fiber.App, string) {
	real, err := imageprocessor.New(context.Background())
	if err != nil {
		b.Fatalf("native processor unavailable: %v", err)
	}
	dir := b.TempDir()
	resolver, err := NewImageResolver(map[string]string{"/images/": dir}, DefaultImageSourceLimits())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = resolver.Close() })
	cache, err := imagecache.Open(imagecache.Config{
		Dir:           filepath.Join(b.TempDir(), "imgcache"),
		MaxTotalBytes: 64 << 20,
		MaxEntries:    1024,
		MaxEntryBytes: 8 << 20,
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = cache.Close() })
	app := fiber.New(fiber.Config{ReadBufferSize: 65536})
	if err := MountImageProcessing(app, imageopt.DefaultLimits(), ImageEndpointConfig{
		Resolver:      resolver,
		Processor:     real,
		MaxConcurrent: 4,
		Cache:         cache,
	}); err != nil {
		b.Fatal(err)
	}
	return app, dir
}

func benchGet(b *testing.B, app *fiber.App, q string) {
	b.Helper()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := app.Test(httptest.NewRequest(http.MethodGet, q, nil))
		if err != nil || res.StatusCode != 200 {
			b.Fatalf("request failed: %v %d", err, res.StatusCode)
		}
		res.Body.Close()
	}
}

func BenchmarkF05WebPMiss(b *testing.B) {
	app, dir := benchServer(b)
	os.WriteFile(filepath.Join(dir, "hero.png"), tinyPNGBench(64, 32), 0o644)
	q := "/_meta/image?src=%2Fimages%2Fhero.png&w=32&f=webp"
	// Each iteration must miss: vary the source content per request.
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		os.WriteFile(filepath.Join(dir, "hero.png"), tinyPNGBench(64, 32+i), 0o644)
		b.StartTimer()
		res, err := app.Test(httptest.NewRequest(http.MethodGet, q, nil))
		if err != nil || res.StatusCode != 200 {
			b.Fatalf("miss failed: %v %d", err, res.StatusCode)
		}
		res.Body.Close()
	}
}

func BenchmarkF05WebPHit(b *testing.B) {
	app, dir := benchServer(b)
	os.WriteFile(filepath.Join(dir, "hero.png"), tinyPNGBench(64, 32), 0o644)
	q := "/_meta/image?src=%2Fimages%2Fhero.png&w=32&f=webp"
	if res, err := app.Test(httptest.NewRequest(http.MethodGet, q, nil)); err != nil || res.StatusCode != 200 {
		b.Fatalf("prime failed: %v", err)
	} else {
		res.Body.Close()
	}
	benchGet(b, app, q)
}

func BenchmarkF05AVIFMiss(b *testing.B) {
	app, dir := benchServer(b)
	os.WriteFile(filepath.Join(dir, "hero.png"), tinyPNGBench(64, 32), 0o644)
	q := "/_meta/image?src=%2Fimages%2Fhero.png&w=32&f=avif"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		os.WriteFile(filepath.Join(dir, "hero.png"), tinyPNGBench(64, 32+i), 0o644)
		b.StartTimer()
		res, err := app.Test(httptest.NewRequest(http.MethodGet, q, nil))
		if err != nil || res.StatusCode != 200 {
			b.Fatalf("miss failed: %v %d", err, res.StatusCode)
		}
		res.Body.Close()
	}
}

func BenchmarkF05AVIFHit(b *testing.B) {
	app, dir := benchServer(b)
	os.WriteFile(filepath.Join(dir, "hero.png"), tinyPNGBench(64, 32), 0o644)
	q := "/_meta/image?src=%2Fimages%2Fhero.png&w=32&f=avif"
	if res, err := app.Test(httptest.NewRequest(http.MethodGet, q, nil)); err != nil || res.StatusCode != 200 {
		b.Fatalf("prime failed: %v", err)
	} else {
		res.Body.Close()
	}
	benchGet(b, app, q)
}
