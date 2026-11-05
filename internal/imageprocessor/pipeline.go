//go:build imageproc_native

package imageprocessor

/*
#include <stdint.h>
#include <stdlib.h>
*/
import "C"

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
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

// headerReadBudget bounds the bytes a stdlib DecodeConfig call may read.
// image.DecodeConfig allocates no pixel buffers, but it does read from
// the provided io.Reader without a limit; a bounded reader makes the
// inspection stage's cost proportional to the header, not the file.
const headerReadBudget = 64 << 10

// rejectAnimatedPNG examines the already size-bounded source snapshot until
// IDAT. APNG requires acTL before IDAT. Bounds are checked before every slice;
// ordinary PNG decoding still verifies the image and its checksums.
func rejectAnimatedPNG(b []byte) error {
	for offset := 8; offset <= len(b)-12; {
		size := uint64(binary.BigEndian.Uint32(b[offset : offset+4]))
		remaining := uint64(len(b) - offset - 12)
		kind := string(b[offset+4 : offset+8])
		if kind == "acTL" {
			return errors.New("animated png not supported")
		}
		if size > remaining {
			return nil // the PNG decoder reports malformed or truncated data
		}
		if kind == "IDAT" {
			return nil
		}
		offset += 12 + int(size)
	}
	return nil // the decoder handles missing image data
}

// pngDecodeConfig parses the PNG IHDR only (stdlib png.DecodeConfig).
func pngDecodeConfig(b []byte) (image.Config, error) {
	return png.DecodeConfig(io.LimitReader(bytes.NewReader(b), headerReadBudget))
}

// jpegDecodeConfig parses the JPEG SOF markers only (stdlib
// jpeg.DecodeConfig).
func jpegDecodeConfig(b []byte) (image.Config, error) {
	return jpeg.DecodeConfig(io.LimitReader(bytes.NewReader(b), headerReadBudget))
}

// toRGBA flattens any image to packed RGBA rows (non-premultiplied 8-bit,
// the convention both WebPPictureImportRGBA and avifRGBImage expect).
func toRGBA(img image.Image) []byte {
	b := img.Bounds()
	dx, dy := b.Dx(), b.Dy()
	out := make([]byte, dx*dy*4)
	// The resizer produces NRGBA, already in the encoders' straight-alpha
	// layout. Copy each row directly, respecting subimages and nonzero bounds.
	if nrgba, ok := img.(*image.NRGBA); ok {
		stride := dx * 4
		for y := 0; y < dy; y++ {
			start := nrgba.PixOffset(b.Min.X, b.Min.Y+y)
			copy(out[y*stride:(y+1)*stride], nrgba.Pix[start:start+stride])
		}
		return out
	}
	i := 0
	for y := 0; y < dy; y++ {
		for x := 0; x < dx; x++ {
			// Color.RGBA returns alpha-premultiplied channels; native encoders
			// consume straight alpha, so convert explicitly before packing.
			px := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			out[i] = px.R
			out[i+1] = px.G
			out[i+2] = px.B
			out[i+3] = px.A
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
