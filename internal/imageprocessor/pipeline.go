//go:build imageproc_native

package imageprocessor

/*
#include <stdint.h>
#include <stdlib.h>
*/
import "C"

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"unsafe"

	xdraw "golang.org/x/image/draw"

	"go-meta/imageproc"
)

// unsafePointer reinterprets a C pointer as an unsafe.Pointer. Internal
// only; no public API exposes C types.
func unsafePointer(p *C.uint8_t) unsafe.Pointer { return unsafe.Pointer(p) }

func copySliceFromC(dst []byte, p *C.uint8_t) {
	copy(dst, unsafe.Slice((*byte)(unsafe.Pointer(p)), len(dst)))
}

// bytesReader adapts []byte for stdlib decoders.
func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// toRGBA flattens any image to packed RGBA rows (non-premultiplied 8-bit,
// the convention both WebPPictureImportRGBA and avifRGBImage expect).
func toRGBA(img image.Image) []byte {
	b := img.Bounds()
	dx, dy := b.Dx(), b.Dy()
	out := make([]byte, dx*dy*4)
	i := 0
	for y := 0; y < dy; y++ {
		for x := 0; x < dx; x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			out[i] = byte(r >> 8)
			out[i+1] = byte(g >> 8)
			out[i+2] = byte(bl >> 8)
			out[i+3] = byte(a >> 8)
			i += 4
		}
	}
	return out
}

// resizeToFit scales img to fit within w×h preserving aspect ratio, never
// upscaling (F03 policy: conservative, deterministic — a small source is
// returned at its original size rather than enlarged and softened). One
// dimension may be zero (unconstrained).
//
// Resampling uses Catmull-Rom interpolation from golang.org/x/image/draw:
// a good-quality, maintained kernel resampler for photographic content,
// correct for both up- and downscaling (though upscaling never triggers
// here), with correct handling of nonzero source bounds — the kernel
// sampler draws into a fresh destination rect and reads source pixels via
// At(), so a source image decoded with an offset origin still samples
// correctly. Alpha: xdraw works on NRGBA destination with
// straight-alpha semantics, so transparent input stays transparent (no
// black flattening; that only happens for JPEG output, documented in
// Process).
func resizeToFit(img image.Image, w, h int) image.Image {
	b := img.Bounds()
	dx, dy := b.Dx(), b.Dy()
	if dx == 0 || dy == 0 {
		return img
	}
	ow, oh := fitDimensions(dx, dy, w, h)
	if ow == dx && oh == dy {
		return img
	}
	dst := image.NewNRGBA(image.Rect(0, 0, ow, oh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
	return dst
}

// fitDimensions computes contain-box dimensions, never upscaling.
// Rounding rule: floor of the scaled dimension, clamped to at least 1 —
// documented in README. Floor (not round-half-up) guarantees the output
// never exceeds the requested bounding box on either axis.
func fitDimensions(dx, dy, w, h int) (int, int) {
	scale := 1.0
	if w > 0 && dx > w {
		scale = float64(w) / float64(dx)
	}
	if h > 0 {
		hs := float64(h) / float64(dy)
		if hs < scale {
			scale = hs
		}
	}
	if scale >= 1.0 {
		return dx, dy
	}
	ow := int(float64(dx) * scale)
	oh := int(float64(dy) * scale)
	if ow < 1 {
		ow = 1
	}
	if oh < 1 {
		oh = 1
	}
	return ow, oh
}

func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	opts := jpeg.Options{Quality: quality}
	if err := jpeg.Encode(&buf, img, &opts); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var _ = imageproc.Source{}
