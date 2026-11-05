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
// upscaling; one dimension may be zero (unconstrained). This is F02's
// placeholder geometry: correct and allocation-bounded, deliberately
// simple (integer source-pixel sampling). F03 replaces it with the real
// resampler without changing the Process contract.
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
	src := toRGBA(img)
	dst := image.NewNRGBA(image.Rect(0, 0, ow, oh))
	for y := 0; y < oh; y++ {
		sy := y * dy / oh
		rowOff := sy * dx * 4
		dRow := dst.Pix[y*dst.Stride : y*dst.Stride+ow*4]
		for x := 0; x < ow; x++ {
			sx := x * dx / ow
			s := src[rowOff+sx*4 : rowOff+sx*4+4]
			d := dRow[x*4 : x*4+4]
			d[0], d[1], d[2], d[3] = s[0], s[1], s[2], s[3]
		}
	}
	return dst
}

// fitDimensions computes contain-box dimensions, never upscaling.
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
