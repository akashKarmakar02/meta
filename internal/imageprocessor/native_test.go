//go:build imageproc_native

package imageprocessor

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"sync"
	"testing"

	"go-meta/imageopt"
	"go-meta/imageproc"
)

// srcJPEG builds a small deterministic JPEG for probes.
func srcJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func src(t *testing.T) imageproc.Source {
	return imageproc.Source{URLIdentity: "/images/probe.jpg", Bytes: srcJPEG(t, 80, 60)}
}

func webpTransform() imageproc.Transform {
	return imageproc.Transform{Width: 40, Height: 40, Quality: 80, Format: imageopt.FormatWebP}
}

// TestNativeInit verifies initialization, capability reporting, and
// version identity.
func TestNativeInit(t *testing.T) {
	n, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	caps := n.Capabilities()
	if !caps.JPEGDecode || !caps.PNGDecode || !caps.WebPEncode || !caps.AVIFEncode ||
		!caps.JPEGEncode || !caps.PNGEncode {
		t.Fatalf("capabilities incomplete: %+v", caps)
	}
	if n.Version() == "" {
		t.Fatal("empty version identity")
	}
	info := n.Info()
	if info.Name == "" || len(info.LibraryInfo) == 0 {
		t.Fatalf("backend info incomplete: %+v", info)
	}
	// Version identity is stable across reads.
	if n.Version() != n.Version() {
		t.Fatal("version identity unstable")
	}
}

// TestNativeWebPAndAVIFEncodeActuallyEncode (§28): prove the actual
// encoders run — outputs decode as their claimed formats with the right
// dimensions. No fabricated results.
func TestNativeWebPAndAVIFEncodeActuallyEncode(t *testing.T) {
	n, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	src := src(t)

	// WebP: encoded output must sniff as WebP and report the resized box.
	res, err := n.Process(context.Background(), src, webpTransform())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Data) < 12 || string(res.Data[:4]) != "RIFF" || string(res.Data[8:12]) != "WEBP" {
		t.Fatalf("output is not a WebP container: %q", res.Data[:min(12, len(res.Data))])
	}
	if res.Width != 40 || res.Height != 30 {
		t.Fatalf("aspect-ratio box wrong: %dx%d", res.Width, res.Height)
	}
	if res.MIME != "image/webp" || res.Format != imageopt.FormatWebP {
		t.Fatalf("format/mime disagreement: %s %s", res.Format, res.MIME)
	}

	// AVIF: encoded output must be a valid ISOBMFF-based AVIF (ftyp box).
	avifT := webpTransform()
	avifT.Format = imageopt.FormatAVIF
	res, err = n.Process(context.Background(), src, avifT)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Data) < 12 || string(res.Data[4:8]) != "ftyp" {
		t.Fatalf("output is not an AVIF ftyp container")
	}
	if res.MIME != "image/avif" {
		t.Fatalf("avif mime: %s", res.MIME)
	}

	// JPEG/PNG outputs must also decode round-trip through stdlib.
	jpgT := webpTransform()
	jpgT.Format = imageopt.FormatJPEG
	res, err = n.Process(context.Background(), src, jpgT)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(res.Data))
	if err != nil || cfg.Width != 40 || cfg.Height != 30 {
		t.Fatalf("jpeg round trip: %v %+v", err, cfg)
	}
}

// TestNativeErrorPaths covers canceled context, unsupported formats,
// invalid transforms, and invalid source bytes.
func TestNativeErrorPaths(t *testing.T) {
	n, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := n.Process(canceled, src(t), webpTransform()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled before work: %v", err)
	}

	badFormat := webpTransform()
	badFormat.Format = imageopt.FormatAuto
	if _, err := n.Process(ctx, src(t), badFormat); !errors.Is(err, imageproc.ErrInvalidTransform) {
		t.Fatalf("FormatAuto accepted by processor: %v", err)
	}

	badDims := webpTransform()
	badDims.Quality = 101
	if _, err := n.Process(ctx, src(t), badDims); !errors.Is(err, imageproc.ErrInvalidTransform) {
		t.Fatalf("invalid quality accepted: %v", err)
	}

	garbage := imageproc.Source{URLIdentity: "/images/x.jpg", Bytes: []byte("not an image")}
	if _, err := n.Process(ctx, garbage, webpTransform()); !errors.Is(err, imageproc.ErrSourceInvalid) {
		t.Fatalf("garbage source accepted: %v", err)
	}

	empty := imageproc.Source{URLIdentity: "/images/x.jpg"}
	if _, err := n.Process(ctx, empty, webpTransform()); !errors.Is(err, imageproc.ErrSourceInvalid) {
		t.Fatalf("empty source accepted: %v", err)
	}
}

// TestNativeResultOwnership proves result bytes are caller-owned: two
// results never share a buffer.
func TestNativeResultOwnership(t *testing.T) {
	n, _ := New(context.Background())
	ctx := context.Background()
	a, err := n.Process(ctx, src(t), webpTransform())
	if err != nil {
		t.Fatal(err)
	}
	b, err := n.Process(ctx, src(t), webpTransform())
	if err != nil {
		t.Fatal(err)
	}
	a.Data[0] ^= 0xff
	if b.Data[0] == a.Data[0] && bytes.Equal(a.Data, b.Data) && &a.Data[0] == &b.Data[0] {
		t.Fatal("results share one buffer")
	}
}

// TestNativeConcurrentUse runs parallel processes under the race detector.
func TestNativeConcurrentUse(t *testing.T) {
	n, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var wg sync.WaitGroup
	errCh := make(chan error, 16)
	for i := range 16 {
		wg.Go(func() {
			tr := webpTransform()
			tr.Width = 20 + i
			tr.Height = 20 + i
			if _, err := n.Process(ctx, src(t), tr); err != nil {
				errCh <- err
			}
		})
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	// Capabilities remain readable concurrently after load.
	if !n.Capabilities().AVIFEncode {
		t.Fatal("capabilities lost under concurrency")
	}
}

// TestNativeShutdownIsSafe closes repeatedly and processes afterwards:
// this backend holds no retained native state, so Close is a no-op and
// use-after-close cannot free anything.
func TestNativeShutdownIsSafe(t *testing.T) {
	n, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
	if err := n.Close(); err != nil {
		t.Fatal("second Close not idempotent:", err)
	}
	if _, err := n.Process(context.Background(), src(t), webpTransform()); err != nil {
		t.Fatalf("process after close failed: %v", err)
	}
}

// TestNativeDecodeConfigSniffsDimensions verifies the F05-facing probe.
func TestNativeDecodeConfigSniffsDimensions(t *testing.T) {
	n, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := n.DecodeConfig(src(t))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 80 || cfg.Height != 60 {
		t.Fatalf("sniffed %dx%d want 80x60", cfg.Width, cfg.Height)
	}
}

// TestNativeNeverUpscales pins the placeholder geometry contract F03 will
// inherit: contain-box only, no enlargement.
func TestNativeNeverUpscales(t *testing.T) {
	n, _ := New(context.Background())
	ctx := context.Background()
	for _, dims := range [][2]int{{800, 600}, {0, 100}, {2000, 0}} {
		tr := imageproc.Transform{Width: dims[0], Height: dims[1], Quality: 80, Format: imageopt.FormatJPEG}
		res, err := n.Process(ctx, src(t), tr)
		if err != nil {
			t.Fatalf("%v: %v", dims, err)
		}
		if res.Width != 80 || res.Height != 60 {
			t.Fatalf("%v: upscaled to %dx%d", dims, res.Width, res.Height)
		}
	}
}
