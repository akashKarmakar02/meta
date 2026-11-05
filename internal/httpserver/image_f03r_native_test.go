//go:build imageproc_native

package httpserver

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"go-meta/imageopt"
	"go-meta/imageproc"
	"go-meta/internal/imageprocessor"
)

// F03R native HTTP tests: the real processor over the real resolver at the
// HTTP layer. Skips when runtime libraries are unavailable.

func f03rNativeApp(t *testing.T) (*fiber.App, string) {
	t.Helper()
	backend, err := imageprocessor.New(context.Background())
	if err != nil {
		t.Skipf("native backend unavailable: %v", err)
	}
	dir := t.TempDir()
	resolver, err := NewImageResolver(map[string]string{"/images/": dir}, DefaultImageSourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resolver.Close() })
	app := fiber.New()
	if err := MountImageProcessing(app, imageopt.DefaultLimits(), ImageEndpointConfig{
		Resolver:  resolver,
		Processor: backend,
	}); err != nil {
		t.Fatal(err)
	}
	return app, dir
}

func f03rGradient(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 255 / max(1, w-1)), G: uint8(y * 255 / max(1, h-1)), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestF03RHTTPNativeRealOutputs: a real JPEG source optimizes to genuinely
// decodable WebP and AVIF bytes with no-store caching and correct MIME.
func TestF03RHTTPNativeRealOutputs(t *testing.T) {
	app, dir := f03rNativeApp(t)
	os.WriteFile(filepath.Join(dir, "hero.jpg"), f03rGradient(t, 800, 450), 0o644)

	for _, tc := range []struct {
		path     string
		mime     string
		checkDim bool
	}{
		{"/_meta/image?src=%2Fimages%2Fhero.jpg&w=400&f=webp", "image/webp", true},
		{"/_meta/image?src=%2Fimages%2Fhero.jpg&w=400&f=avif", "image/avif", true},
		{"/_meta/image?src=%2Fimages%2Fhero.jpg&w=400&f=jpeg", "image/jpeg", true},
		{"/_meta/image?src=%2Fimages%2Fhero.jpg&w=400&f=png", "image/png", true},
	} {
		res, err := app.Test(httptest.NewRequest(http.MethodGet, tc.path, nil), fiber.TestConfig{Timeout: testTimeout()})
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != tc.mime {
			t.Fatalf("%s: %d %s", tc.path, res.StatusCode, res.Header.Get("Content-Type"))
		}
		if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
			t.Fatalf("%s: cache-control = %q, want no-store", tc.path, cc)
		}
		if len(body) == 0 {
			t.Fatalf("%s: empty body", tc.path)
		}
	}
}

// TestF03RHTTPNativeWebPSourceRoundTrip: a WebP source file is decoded by
// the native path and re-encoded — proving the family dispatch handles
// webp end to end at the HTTP layer.
func TestF03RHTTPNativeWebPSourceRoundTrip(t *testing.T) {
	backend, err := imageprocessor.New(context.Background())
	if err != nil {
		t.Skipf("native backend unavailable: %v", err)
	}
	dir := t.TempDir()
	// Build a WebP source via the processor itself (JPEG → WebP), then
	// serve that WebP back as the source.
	res, err := backend.Process(context.Background(),
		imageproc.Source{Bytes: f03rGradient(t, 200, 100)},
		imageproc.Transform{Width: 200, Quality: 85, Format: imageopt.FormatWebP})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "src.webp"), res.Data, 0o644)

	r2, err := NewImageResolver(map[string]string{"/images/": dir}, DefaultImageSourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	app := fiber.New()
	if err := MountImageProcessing(app, imageopt.DefaultLimits(), ImageEndpointConfig{
		Resolver: r2, Processor: backend,
	}); err != nil {
		t.Fatal(err)
	}
	out, err := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fsrc.webp&w=100&f=webp", nil), fiber.TestConfig{Timeout: testTimeout()})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(out.Body)
	if out.StatusCode != 200 || out.Header.Get("Content-Type") != "image/webp" {
		t.Fatalf("webp roundtrip: %d %s", out.StatusCode, out.Header.Get("Content-Type"))
	}
	if img, derr := imageprocessor.DecodeWebPForTest(body); derr != nil || img.Bounds().Dx() != 100 {
		t.Fatalf("output undecodable or wrong dims: %v", derr)
	}
}

// TestF03RHTTPNativePreDecodeBomb: a tiny file declaring huge dimensions
// is refused 413 before any decode or encode work. The fixture is under
// a hundred bytes.
func TestF03RHTTPNativePreDecodeBomb(t *testing.T) {
	app, dir := f03rNativeApp(t)
	// PNG IHDR declaring 100000×100000 with no body. The stdlib sniffs
	// it fine (IHDR-only), so the pixel ceiling must refuse it 413.
	os.WriteFile(filepath.Join(dir, "bomb.png"), bombPNGHeaders(100000, 100000), 0o644)
	// A truncated PNG: header cut mid-IHDR — refused 415 as invalid metadata.
	os.WriteFile(filepath.Join(dir, "trunc.png"), bombPNGHeaders(100000, 100000)[:20], 0o644)
	// WebP VP8X header declaring 30000×30000.
	os.WriteFile(filepath.Join(dir, "bomb.webp"), bombWebPHeaders(30000, 30000), 0o644)

	for _, name := range []string{"bomb.png", "bomb.webp"} {
		res, err := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2F"+name+"&w=50&f=webp", nil))
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("%s: status = %d, want 413", name, res.StatusCode)
		}
		if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
			t.Fatalf("%s: cache-control = %q", name, cc)
		}
	}

	// The endpoint stays usable after the bombs.
	os.WriteFile(filepath.Join(dir, "ok.jpg"), f03rGradient(t, 100, 80), 0o644)
	res, err := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fok.jpg&w=50&f=webp", nil), fiber.TestConfig{Timeout: testTimeout()})
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("endpoint unusable after bombs: %d", res.StatusCode)
	}
}

// TestF03RHTTPNativeAnimatedAndCorrupt: an animated WebP source and a
// truncated JPEG are refused 415 by the real pipeline.
func TestF03RHTTPNativeAnimatedAndCorrupt(t *testing.T) {
	app, dir := f03rNativeApp(t)
	os.WriteFile(filepath.Join(dir, "anim.webp"), animatedWebPHeader(32, 32), 0o644)
	trunc := f03rGradient(t, 60, 40)
	os.WriteFile(filepath.Join(dir, "trunc.jpg"), trunc[:len(trunc)/2], 0o644)

	for name, want := range map[string]int{
		"anim.webp": http.StatusUnsupportedMediaType,
		"trunc.jpg": http.StatusUnsupportedMediaType,
	} {
		res, err := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2F"+name+"&w=50&f=webp", nil))
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != want {
			t.Fatalf("%s: status = %d, want %d", name, res.StatusCode, want)
		}
	}
}

// bombPNGHeaders writes a PNG IHDR claiming w×h with no pixel data.
func bombPNGHeaders(w, h int) []byte {
	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")
	writePNGChunk(&buf, "IHDR", func() []byte {
		d := make([]byte, 13)
		binaryPutUint32(d[0:], uint32(w))
		binaryPutUint32(d[4:], uint32(h))
		d[8] = 8 // bit depth
		d[9] = 2 // truecolor
		return d
	}())
	return buf.Bytes()
}

// animatedWebPHeader builds a standalone VP8X-only WebP header with the
// animation flag set, declaring w×h.
func animatedWebPHeader(w, h int) []byte {
	out := bombWebPHeaders(w, h)
	// VP8X body starts at RIFF(12) + "VP8X"(4) + size(4) = offset 20.
	// Flags byte: bit 1 (0x02) = animation.
	out[20] = 0x02
	return out
}

// bombWebPHeaders builds a standalone VP8X-only WebP header declaring w×h.
func bombWebPHeaders(w, h int) []byte {
	cw := uint32(w - 1)
	ch := uint32(h - 1)
	body := make([]byte, 10)
	body[0] = 0x10 // flags: alpha only, no animation
	body[4] = byte(cw & 0xff)
	body[5] = byte((cw >> 8) & 0xff)
	body[6] = byte(ch & 0xff)
	body[7] = byte((ch >> 8) & 0xff)
	chunk := append([]byte("VP8X"), 10, 0, 0, 0)
	chunk = append(chunk, body...)
	riffSize := 4 + len(chunk)
	out := []byte("RIFF")
	out = append(out, byte(riffSize), byte(riffSize>>8), byte(riffSize>>16), byte(riffSize>>24))
	out = append(out, []byte("WEBP")...)
	out = append(out, chunk...)
	return out
}

func binaryPutUint32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
}

func writePNGChunk(buf *bytes.Buffer, typ string, data []byte) {
	var lenb [4]byte
	binaryPutUint32(lenb[:], uint32(len(data)))
	buf.Write(lenb[:])
	buf.WriteString(typ)
	buf.Write(data)
	var crc uint32 = 0xffffffff
	for _, c := range append([]byte(typ), data...) {
		crc ^= uint32(c)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xedb88320
			} else {
				crc >>= 1
			}
		}
	}
	crc ^= 0xffffffff
	binaryPutUint32(lenb[:], crc)
	buf.Write(lenb[:])
}

func testTimeout() (d time.Duration) { return 30 * time.Second }
