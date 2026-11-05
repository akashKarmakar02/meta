//go:build imageproc_native

package imageprocessor

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	"go-meta/imageopt"
	"go-meta/imageproc"
)

// FuzzInspectSource fuzzes the bounded pre-decode inspection with SMALL
// inputs only (len(src) ≤ 4 KiB enforced in the target): the inspectors
// are header parsers, and the F03R regression suite already proves
// full-decode rejection. The invariant: no panic, and any accepted
// inspection must carry positive, within-ceiling dimensions.
func FuzzInspectSource(f *testing.F) {
	smallPNG := pngSeed()
	for _, seed := range [][]byte{
		smallPNG,
		{0xff, 0xd8, 0xff, 0xe0},
		vp8xHeaderWebP(100, 80),
		vp8xHeaderWebP(30000, 30000),
		[]byte("GIF89a"),
		[]byte("\x00\x00\x00\x14ftypavif"),
		[]byte("RIFF\x24\x00\x00\x00WEBPVP8X"),
		[]byte("junk"),
		{},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > 4096 {
			src = src[:4096] // keep the corpus header-sized
		}
		n, err := New(nil)
		if err != nil {
			t.Skip("native unavailable")
		}
		ins, ierr := n.inspectSource(src)
		if ierr != nil {
			return // rejected safely
		}
		if ins.width <= 0 || ins.height <= 0 {
			t.Fatalf("inspection accepted %dx%d", ins.width, ins.height)
		}
		if int64(ins.width)*int64(ins.height) > maxDecodedPixels {
			t.Fatalf("inspection accepted %dx%d over ceiling", ins.width, ins.height)
		}
	})
}

// FuzzProcessSmall runs the full pipeline on tiny fuzzed inputs. This is
// deliberately NOT unrestricted native fuzzing: inputs are capped at 2 KiB
// so no allocation can be large; the goal is panic-freedom of the whole
// path, not decoder robustness at scale.
func FuzzProcessSmall(f *testing.F) {
	for _, seed := range [][]byte{
		pngSeed(),
		{},
		[]byte("x"),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > 2048 {
			src = src[:2048]
		}
		n, err := New(nil)
		if err != nil {
			t.Skip("native unavailable")
		}
		tr := imageproc.Transform{Width: 8, Quality: 50, Format: imageopt.FormatWebP}
		_, _ = n.Process(context.Background(), imageproc.Source{Bytes: src}, tr)
	})
}

// pngSeed builds a small valid PNG without a testing.T (fuzz seeds run
// before the harness is live).
func pngSeed() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 16), G: uint8(y * 16), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}
