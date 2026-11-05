//go:build imageproc_native

package imageprocessor

import (
	"image"
	"testing"
)

// F04 §22: pathological aspect ratios, tiny dims, and extreme boxes for
// the contain geometry — no overflow, no zero axis, never upscale, always
// inside the box.
func TestF04FitDimensionsPathological(t *testing.T) {
	rows := []struct {
		name         string
		dx, dy, w, h int
		wantW, wantH int
	}{
		{"wide-panorama-in-tiny-box", 10000, 1, 100, 100, 100, 1},
		{"tall-strip-in-tiny-box", 1, 10000, 100, 100, 1, 100},
		{"extreme-wide", 4000, 1, 4096, 4096, 4000, 1},
		{"extreme-tall", 1, 4000, 4096, 4096, 1, 4000},
		{"one-pixel-source", 1, 1, 100, 100, 1, 1},
		{"no-upscale-exact", 500, 500, 500, 500, 500, 500},
		{"no-upscale-larger-box", 500, 500, 4096, 4096, 500, 500},
		{"width-only", 800, 600, 400, 0, 400, 300},
		{"height-only", 800, 600, 0, 150, 200, 150},
		// 999×999 into 500×500: scale is exactly 500/999; floor of
		// 999*(500/999) lands on 500 (float), within the box.
		{"floor-never-exceeds-box", 999, 999, 500, 500, 500, 500},
		{"clamp-to-one", 100000, 2, 10, 10, 10, 1},
		// 1920×1080 into width-1 box: scale 1/1920 floors both axes to
		// the ≥1 clamp: a 1×1 thumbnail of a 16:9 image. Aspect is
		// unrepresentable at one pixel; the floor+clamp rule documents it.
		{"very-narrow-box", 1920, 1080, 1, 4096, 1, 1},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			gotW, gotH := fitDimensions(r.dx, r.dy, r.w, r.h)
			if gotW != r.wantW || gotH != r.wantH {
				t.Fatalf("fitDimensions(%d,%d,%d,%d) = %d×%d, want %d×%d",
					r.dx, r.dy, r.w, r.h, gotW, gotH, r.wantW, r.wantH)
			}
			if gotW < 1 || gotH < 1 {
				t.Fatalf("zero axis output: %d×%d", gotW, gotH)
			}
			if r.w > 0 && gotW > r.w {
				t.Fatalf("width exceeds box: %d > %d", gotW, r.w)
			}
			if r.h > 0 && gotH > r.h {
				t.Fatalf("height exceeds box: %d > %d", gotH, r.h)
			}
			if gotW > r.dx || gotH > r.dy {
				t.Fatalf("upscaled: %d×%d from %d×%d", gotW, gotH, r.dx, r.dy)
			}
		})
	}
}

// resizeToFit on a degenerate source must not panic.
func TestF04ResizeToFitDegenerateNoPanic(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 0, 0))
	got := resizeToFit(img, 100, 100)
	if got.Bounds().Dx() != 0 {
		t.Fatal("degenerate resize produced output")
	}
}
