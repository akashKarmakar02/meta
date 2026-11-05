//go:build imageproc_native

package imageprocessor

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"sync/atomic"
	"testing"

	"github.com/akashKarmakar02/meta/imageopt"
	"github.com/akashKarmakar02/meta/imageproc"
)

// Pre-decode gate regression tests (F03R §4). The fixtures are tiny files
// with hostile declared dimensions; the decoding seam (n.fullDecoder)
// proves the full decoder and the native encoders are never invoked for
// sources rejected at or before inspection.

// bombPNG returns a PNG that declares w×h in its IHDR but carries no
// pixel data: a few dozen bytes whose declared dimensions can exceed any
// limit. Compressed size is irrelevant to decode cost, which is why the
// dimension check must happen before allocation.
func bombPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	return hugeDimensionsPNG(t, w, h)
}

// countingDecoder installs the decoding seam: every invocation delegates
// to the real family decoder and increments the call counter.
func countingDecoder(n *Native) *atomic.Int32 {
	var calls atomic.Int32
	n.fullDecoder = func(b []byte, format string) (image.Image, error) {
		calls.Add(1)
		return decodeFamily(b, format)
	}
	return &calls
}

// processWithSeam runs Process with the seam installed and reports the
// full-decoder call count.
func processWithSeam(t *testing.T, n *Native, src []byte, tr imageproc.Transform) (imageproc.Result, error, int32) {
	t.Helper()
	calls := countingDecoder(n)
	res, err := n.Process(context.Background(), imageproc.Source{Bytes: src}, tr)
	return res, err, calls.Load()
}

func predecodeTransform() imageproc.Transform {
	return imageproc.Transform{Width: 32, Quality: 80, Format: imageopt.FormatWebP}
}

// TestF03RPreDecodeRejectionTable covers the §4 boundary matrix. Every
// rejected row must fail with zero full-decoder calls; the at-limit row
// must pass inspection and reach the decoder exactly once.
func TestF03RPreDecodeRejectionTable(t *testing.T) {
	n := f03Backend(t)

	// Exactly the permitted pixel count (6324×6325 = 39,999,900 ≤ 40MP):
	// the gate must pass it through to the decoder exactly once (the
	// fixture carries no pixel data, so the decode itself then fails —
	// which still proves the pixel ceiling did not reject it early).
	atLimit := bombPNG(t, 6324, 6325)
	// One pixel beyond the limit (6325×6325 = 40,005,625 > 40MP).
	oneBeyond := bombPNG(t, 6325, 6325)
	zeroDimPNG := hugeDimensionsPNG(t, 0, 100)

	rows := []struct {
		name string
		src  []byte
		want error // nil: must be accepted through the gate
	}{
		{name: "exactly-at-pixel-limit", src: atLimit, want: imageproc.ErrSourceInvalid},
		{name: "one-pixel-beyond-limit", src: oneBeyond, want: imageproc.ErrSourceTooLarge},
		{name: "zero-dimension", src: zeroDimPNG, want: imageproc.ErrSourceInvalid},
		{name: "extremely-large-declared", src: bombPNG(t, 1<<20, 1<<20), want: imageproc.ErrSourceTooLarge},
		{name: "truncated-metadata", src: validPNG(t)[:16], want: imageproc.ErrSourceInvalid},
		// A header declaring modest dimensions but no decodable body passes
		// the gate and is then refused by the full decode itself.
		{name: "declared-valid-but-body-invalid", src: bombPNG(t, 100, 100), want: imageproc.ErrSourceInvalid},
		{name: "unsupported-animation-gif", src: validGIF(), want: imageproc.ErrSourceInvalid},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			res, err, decoderCalls := processWithSeam(t, n, row.src, predecodeTransform())
			if err == nil {
				t.Fatal("rejected-case source accepted")
			}
			if !errors.Is(err, row.want) {
				t.Fatalf("err = %v, want Is(%v)", err, row.want)
			}
			if row.name == "declared-valid-but-body-invalid" || row.name == "exactly-at-pixel-limit" {
				// These rows legitimately pass the pixel/dimension gate and
				// reach the decoder once before failing on the missing body —
				// proving the gate did not pre-reject them. Everything else
				// must be refused before any decode.
				if decoderCalls != 1 {
					t.Fatalf("decoder calls = %d, want 1", decoderCalls)
				}
			} else if decoderCalls != 0 {
				t.Fatalf("full decoder invoked %d times for rejected source", decoderCalls)
			}
			if len(res.Data) != 0 {
				t.Fatal("rejected source produced output bytes")
			}
			// The processor must remain usable after rejection.
			okRes, okErr, _ := processWithSeam(t, n, validPNG(t), predecodeTransform())
			if okErr != nil {
				t.Fatalf("processor unusable after rejection: %v", okErr)
			}
			if len(okRes.Data) == 0 {
				t.Fatal("no output bytes after recovery")
			}
		})
	}
}

// TestF03RExactPixelBoundary: checkedInspection accepts exactly
// maxDecodedPixels and refuses one pixel more, overflow-safely.
func TestF03RExactPixelBoundary(t *testing.T) {
	// 40,000,000 = 6250 × 6400 exactly.
	if _, err := checkedInspection("png", 6250, 6400, false); err != nil {
		t.Fatalf("exactly-at-limit rejected: %v", err)
	}
	if _, err := checkedInspection("png", 6251, 6400, false); !errors.Is(err, imageproc.ErrSourceTooLarge) {
		t.Fatalf("one-beyond not refused: %v", err)
	}
	// Extreme declared dimensions must fail the pixel ceiling (via int64
	// product, no overflow at int width either).
	for _, dim := range [][2]int{{1 << 21, 1 << 21}, {1 << 30, 4}, {4, 1 << 30}} {
		if _, err := checkedInspection("png", dim[0], dim[1], false); !errors.Is(err, imageproc.ErrSourceTooLarge) {
			t.Fatalf("%dx%d not refused: %v", dim[0], dim[1], err)
		}
	}
}

// TestF03RWebPBomb: a WebP header declaring huge dimensions is refused
// before the native decoder allocates. VP8X canvas fields are stored as
// 24-bit (w-1)/(h-1); a real 20000×20000 claim needs bits beyond 14, so
// build the header with the wider layout WebPGetFeatures accepts.
func TestF03RWebPBomb(t *testing.T) {
	n := f03Backend(t)
	for _, dim := range []int{20000 /* > 40MP */, 30000} {
		bomb := vp8xHeaderWebP(dim, dim)
		_, err, decoderCalls := processWithSeam(t, n, bomb, predecodeTransform())
		if err == nil {
			t.Fatalf("webp bomb %dx%d accepted", dim, dim)
		}
		if !errors.Is(err, imageproc.ErrSourceTooLarge) {
			t.Fatalf("%dx%d: err = %v, want ErrSourceTooLarge", dim, dim, err)
		}
		if decoderCalls != 0 {
			t.Fatalf("full decoder invoked %d times for webp bomb", decoderCalls)
		}
	}
}

// TestF03RValidSmallImageStillWorks: the seam delegates to the real
// pipeline, so a valid source round-trips with exactly one decode.
func TestF03RValidSmallImageStillWorks(t *testing.T) {
	n := f03Backend(t)
	res, err, decoderCalls := processWithSeam(t, n, validPNG(t), predecodeTransform())
	if err != nil {
		t.Fatal(err)
	}
	if decoderCalls != 1 {
		t.Fatalf("decoder calls = %d, want 1", decoderCalls)
	}
	if len(res.Data) == 0 || res.Format != imageopt.FormatWebP {
		t.Fatalf("no real webp output: %d bytes", len(res.Data))
	}
	if dw, dh, derr := webpOutputDims(res.Data); derr != nil || dw != 32 || dh != 32 {
		t.Fatalf("decoded dims %dx%d %v", dw, dh, derr)
	}
}

// TestF03RHeaderDecodeDisagreement: a decoded frame whose dimensions
// differ from the inspected header must be refused (post-decode
// revalidation). Simulated via the seam returning a mismatched frame.
func TestF03RHeaderDecodeDisagreement(t *testing.T) {
	n := f03Backend(t)
	n.fullDecoder = func(b []byte, format string) (image.Image, error) {
		// Header says 64×64; return a differently sized frame.
		return image.NewNRGBA(image.Rect(0, 0, 128, 128)), nil
	}
	_, err := n.Process(context.Background(),
		imageproc.Source{Bytes: validPNG(t)}, predecodeTransform())
	if err == nil {
		t.Fatal("dimension disagreement accepted")
	}
	if !errors.Is(err, imageproc.ErrSourceInvalid) {
		t.Fatalf("err = %v, want ErrSourceInvalid", err)
	}
}

// TestF03RDecodeConfigMatchesProcessGate: DecodeConfig (the endpoint's
// pre-decode sniff) applies the same family dispatch and limits as
// Process — the two paths cannot disagree about accept/reject.
func TestF03RDecodeConfigMatchesProcessGate(t *testing.T) {
	n := f03Backend(t)
	for name, src := range map[string][]byte{
		"valid-png":  validPNG(t),
		"valid-jpeg": makeSource(t, 40, 30),
		"bomb-png":   bombPNG(t, 1<<18, 1<<18),
		"svg":        []byte("<svg xmlns='http://www.w3.org/2000/svg' width='10' height='10'/>"),
		"random":     bytes.Repeat([]byte{0x37}, 64),
		"gif":        validGIF(),
	} {
		_, cfgErr := n.DecodeConfig(imageproc.Source{Bytes: src})
		_, procErr := n.Process(context.Background(),
			imageproc.Source{Bytes: src}, predecodeTransform())
		if (cfgErr == nil) != (procErr == nil) {
			t.Fatalf("%s: DecodeConfig err=%v but Process err=%v", name, cfgErr, procErr)
		}
	}
}

// helpers ----

// validPNG encodes a small deterministic gradient PNG.
func validPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 4), G: uint8(y * 4), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// vp8xHeaderWebP builds a standalone VP8X-only WebP header declaring w×h.
func vp8xHeaderWebP(w, h int) []byte {
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

// validGIF builds a minimal GIF89a header (2×2, global color table) — an
// unsupported input family in F03.
func validGIF() []byte {
	header := []byte("GIF89a")
	lsd := []byte{2, 0, 2, 0, 0x80, 0, 0} // 2x2 logical screen, GCT flag
	gct := make([]byte, 6)                // 2-color palette
	trailer := []byte{0x3b}
	out := append([]byte{}, header...)
	out = append(out, lsd...)
	out = append(out, gct...)
	out = append(out, trailer...)
	return out
}
