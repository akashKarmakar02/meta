//go:build imageproc_native

// F06 §46–52: realistic performance measurement. Unlike the F05
// directional benchmarks (a 64×32 PNG only), this matrix covers tiny
// through several-megapixel sources, transparent content, and a
// photograph-like JPEG — the workloads operators actually transform. All
// fixtures are deterministic generated images; nothing large is committed.
//
// Measurement conditions (documented honestly): single machine, no -race,
// GOMAXPROCS default. Timings are per-operation averages under go test -bench;
// they characterize relative cost on THIS hardware, not universal
// promises. Warm OS page cache is inherent to the in-process loop.
package httpserver

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/akashKarmakar02/meta/imageopt"
	"github.com/akashKarmakar02/meta/imageproc"
	"github.com/akashKarmakar02/meta/internal/imagecache"
	"github.com/akashKarmakar02/meta/internal/imageprocessor"
)

var f06SourceVersion atomic.Uint64

type f06CountingProcessor struct {
	*imageprocessor.Native
	calls, active, peak atomic.Int64
}

func (p *f06CountingProcessor) Process(ctx context.Context, src imageproc.Source, tr imageproc.Transform) (imageproc.Result, error) {
	p.calls.Add(1)
	n := p.active.Add(1)
	for old := p.peak.Load(); n > old && !p.peak.CompareAndSwap(old, n); old = p.peak.Load() {
	}
	defer p.active.Add(-1)
	return p.Native.Process(ctx, src, tr)
}

// benchFixture is one workload-matrix entry (F06 §47).
type benchFixture struct {
	name string // short name for benchmark labels
	w, h int    // source dimensions
	data []byte // encoded source bytes (deterministic)
}

// f06Fixtures builds the deterministic workload matrix. Photograph-like
// sources use smooth low-frequency color fields plus deterministic noise,
// which exercises resampling cost like real photographic content without
// committing camera fixtures to the repo.
//
// Matrix (§47): A tiny 64×32, B small 640×360, C medium 1920×1080,
// D larger-but-safe 6 MP within limits, E transparent PNG, F photo-like JPEG.
func f06Fixtures(b *testing.B) []benchFixture {
	b.Helper()
	mk := func(w, h int, transparent bool) image.Image {
		img := image.NewNRGBA(image.Rect(0, 0, w, h))
		rng := rand.New(rand.NewSource(int64(w*31 + h)))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				// Smooth gradient base + deterministic per-pixel noise keeps
				// encoders from hitting degenerate flat-block fast paths.
				base := uint8((x*7 + y*13) & 0xff)
				n := uint8(rng.Intn(32))
				a := uint8(255)
				if transparent && ((x/8+y/8)&1) == 0 {
					a = uint8(rng.Intn(256)) // varied alpha regions
				}
				img.SetNRGBA(x, y, color.NRGBA{R: base + n, G: base - n/2, B: n * 3, A: a})
			}
		}
		return img
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, mk(640, 360, false)); err != nil {
		b.Fatal(err)
	}
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, mk(1920, 1080, false), &jpeg.Options{Quality: 90}); err != nil {
		b.Fatal(err)
	}
	var bigBuf bytes.Buffer
	if err := jpeg.Encode(&bigBuf, mk(3000, 2000, false), &jpeg.Options{Quality: 85}); err != nil {
		b.Fatal(err) // 6 MP photograph-like source, ~several hundred KB
	}
	var alphaBuf bytes.Buffer
	if err := png.Encode(&alphaBuf, mk(640, 360, true)); err != nil {
		b.Fatal(err)
	}
	return []benchFixture{
		{"tiny", 64, 32, tinyPNGBench(64, 32)},
		{"small", 640, 360, pngBuf.Bytes()},
		{"medium", 1920, 1080, jpegBuf.Bytes()},
		{"large6mp", 3000, 2000, bigBuf.Bytes()},
		{"alpha", 640, 360, alphaBuf.Bytes()},
	}
}

// f06BenchServer builds the same endpoint wiring as production: real
// native processor, disk cache in a temp dir, concurrency 4.
func f06BenchServer(b *testing.B) (*fiber.App, string, *imagecache.Cache, *f06CountingProcessor) {
	b.Helper()
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
		MaxTotalBytes: 256 << 20,
		MaxEntries:    4096,
		MaxEntryBytes: 64 << 20,
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = cache.Close() })
	app := fiber.New(fiber.Config{ReadBufferSize: 65536})
	proc := &f06CountingProcessor{Native: real}
	if err := MountImageProcessing(app, imageopt.DefaultLimits(), ImageEndpointConfig{
		Resolver:      resolver,
		Processor:     proc,
		MaxConcurrent: 4,
		Cache:         cache,
	}); err != nil {
		b.Fatal(err)
	}
	return app, dir, cache, proc
}

// f06Get issues one request and asserts success.
func f06Get(b *testing.B, app *fiber.App, q string) []byte {
	b.Helper()
	res, err := app.Test(httptest.NewRequest(http.MethodGet, q, nil))
	if err != nil || res.StatusCode != 200 {
		b.Fatalf("request failed: %v %d", err, res.StatusCode)
	}
	var buf bytes.Buffer
	buf.ReadFrom(res.Body)
	res.Body.Close()
	return buf.Bytes()
}

// BenchmarkF06WorkloadMatrix measures cache-miss (full pipeline) versus
// cache-hit latency per workload and output format (§48). Each miss
// iteration rewrites the source with unique content so it genuinely misses;
// hit iterations keep the source fixed after priming.
func BenchmarkF06WorkloadMatrix(b *testing.B) {
	app, dir, _, _ := f06BenchServer(b)
	fixtures := f06Fixtures(b)
	cases := []struct {
		format string
		w      int
	}{{"webp", 480}, {"avif", 480}, {"jpeg", 480}, {"png", 480}}
	for _, fx := range fixtures {
		srcPath := filepath.Join(dir, fx.name+".png")
		if fx.name == "medium" || fx.name == "large6mp" {
			srcPath = filepath.Join(dir, fx.name+".jpg")
		}
		for _, tc := range cases {
			q := fmt.Sprintf("/_meta/image?src=%%2Fimages%%2F%s&w=%d&f=%s",
				filepath.Base(srcPath), tc.w, tc.format)

			b.Run(fmt.Sprintf("src=%s/out=%s/miss", fx.name, tc.format), func(b *testing.B) {
				if err := os.WriteFile(srcPath, fx.data, 0o644); err != nil {
					b.Fatal(err)
				}
				b.ResetTimer()
				var outputBytes int
				for i := 0; i < b.N; i++ {
					// Keep decoded pixels unchanged while changing the complete
					// source fingerprint; PNG and JPEG allow trailing bytes.
					b.StopTimer()
					data := append(bytes.Clone(fx.data), make([]byte, 8)...)
					binary.BigEndian.PutUint64(data[len(data)-8:], f06SourceVersion.Add(1))
					if err := os.WriteFile(srcPath, data, 0o644); err != nil {
						b.Fatal(err)
					}
					b.StartTimer()
					outputBytes = len(f06Get(b, app, q))
				}
				b.ReportMetric(float64(len(fx.data)+8), "source-B")
				b.ReportMetric(float64(outputBytes), "output-B")
			})
			b.Run(fmt.Sprintf("src=%s/out=%s/hit", fx.name, tc.format), func(b *testing.B) {
				os.WriteFile(srcPath, fx.data, 0o644)
				outputBytes := len(f06Get(b, app, q)) // prime the cache entry
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					f06Get(b, app, q)
				}
				b.ReportMetric(float64(len(fx.data)), "source-B")
				b.ReportMetric(float64(outputBytes), "output-B")
			})
		}
	}
}

// BenchmarkF06HashingCost measures the full disk-hit path, including source
// reads, header inspection, SHA-256, cache reads, and the Fiber adapter.
func BenchmarkF06HashingCost(b *testing.B) {
	app, dir, _, _ := f06BenchServer(b)
	sizes := []struct {
		name  string
		fixFn func() []byte
	}{
		{"tiny-png", func() []byte { return tinyPNGBench(64, 32) }},
	}
	// Additional sizes derived from the fixture matrix.
	fixtures := f06Fixtures(b)
	var smallPNG, mediumJPG, largeJPG []byte
	for _, f := range fixtures {
		switch f.name {
		case "small":
			smallPNG = f.data
		case "medium":
			mediumJPG = f.data
		case "large6mp":
			largeJPG = f.data
		}
	}
	sizes = append(sizes,
		struct {
			name  string
			fixFn func() []byte
		}{"small-png", func() []byte { return smallPNG }},
		struct {
			name  string
			fixFn func() []byte
		}{"medium-JPEG", func() []byte { return mediumJPG }},
		struct {
			name  string
			fixFn func() []byte
		}{"large-6MP-JPEG", func() []byte { return largeJPG }},
	)
	for _, s := range sizes {
		data := s.fixFn()
		if len(data) == 0 {
			continue
		}
		b.Run(s.name, func(b *testing.B) {
			srcPath := filepath.Join(dir, "hash.png")
			os.WriteFile(srcPath, data, 0o644)
			q := "/_meta/image?src=%2Fimages%2Fhash.png&w=480&f=webp"
			f06Get(b, app, q) // prime
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				f06Get(b, app, q)
			}
			b.ReportMetric(float64(len(data)), "source-B")
		})
	}
}

// BenchmarkF06ConcurrentBursts runs synchronized bursts of N parallel
// requests over mixed hit/miss workloads (§51). Miss bursts rewrite the
// source between waves. It reports wall-clock completion of the whole
// burst (b.N bursts) at each concurrency level.
func BenchmarkF06ConcurrentBursts(b *testing.B) {
	app, dir, _, proc := f06BenchServer(b)
	fixtures := f06Fixtures(b)
	var small []byte
	for _, f := range fixtures {
		if f.name == "small" {
			small = f.data
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "burst.png"), small, 0o644); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hit.png"), small, 0o644); err != nil {
		b.Fatal(err)
	}
	hitQ := "/_meta/image?src=%2Fimages%2Fhit.png&w=320&f=webp"
	f06Get(b, app, hitQ) // prime one entry

	for _, level := range []int{1, 2, 4, 8, 16} {
		b.Run(fmt.Sprintf("hit/concurrency=%d", level), func(b *testing.B) {
			var wg sync.WaitGroup
			start := proc.calls.Load()
			proc.peak.Store(0)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				wg.Add(level)
				for j := 0; j < level; j++ {
					go func() {
						defer wg.Done()
						f06Get(b, app, hitQ)
					}()
				}
				wg.Wait()
			}
			b.ReportMetric(float64(proc.calls.Load()-start)/float64(b.N), "process/burst")
			b.ReportMetric(float64(proc.peak.Load()), "peak-process")
		})
		b.Run(fmt.Sprintf("samekeymiss/concurrency=%d", level), func(b *testing.B) {
			var wg sync.WaitGroup
			start := proc.calls.Load()
			proc.peak.Store(0)
			q := "/_meta/image?src=%2Fimages%2Fburst.png&w=320&f=webp"
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				data := append(bytes.Clone(small), make([]byte, 8)...)
				binary.BigEndian.PutUint64(data[len(data)-8:], f06SourceVersion.Add(1))
				if err := os.WriteFile(filepath.Join(dir, "burst.png"), data, 0o644); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				wg.Add(level)
				for range level {
					go func() { defer wg.Done(); f06Get(b, app, q) }()
				}
				wg.Wait()
			}
			b.ReportMetric(float64(proc.calls.Load()-start)/float64(b.N), "process/burst")
			b.ReportMetric(float64(proc.peak.Load()), "peak-process")
		})
		b.Run(fmt.Sprintf("mixedmiss/concurrency=%d", level), func(b *testing.B) {
			missQs := make([]string, level)
			for j := range missQs {
				if j%2 == 0 {
					missQs[j] = hitQ
				} else {
					missQs[j] = fmt.Sprintf("/_meta/image?src=%%2Fimages%%2Fburst.png&w=%d&f=webp", 160+j)
				}
			}
			var wg sync.WaitGroup
			start := proc.calls.Load()
			proc.peak.Store(0)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				data := append(bytes.Clone(small), make([]byte, 8)...)
				binary.BigEndian.PutUint64(data[len(data)-8:], f06SourceVersion.Add(1))
				if err := os.WriteFile(filepath.Join(dir, "burst.png"), data, 0o644); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				wg.Add(level)
				for j := 0; j < level; j++ {
					go func(q string) {
						defer wg.Done()
						f06Get(b, app, q)
					}(missQs[j])
				}
				wg.Wait()
			}
			b.ReportMetric(float64(proc.calls.Load()-start)/float64(b.N), "process/burst")
			b.ReportMetric(float64(proc.peak.Load()), "peak-process")
		})
	}
}
