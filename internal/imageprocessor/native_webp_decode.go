//go:build imageproc_native

package imageprocessor

/*
#include <stdlib.h>
#include <string.h>
#include <webp/decode.h>

// webpDecodeRGBA fully decodes a WebP bitstream into packed RGBA rows.
// Returns a malloc'd buffer the caller frees; *out_len receives its size
// (width*height*4). Returns NULL on failure. has_animation reports the
// bitstream's animation flag so callers can reject animated sources
// instead of silently optimizing frame 0.
static unsigned char* webpDecodeRGBA(const uint8_t* data, size_t len,
	int* w, int* h, size_t* out_len, int* has_animation) {
	WebPBitstreamFeatures f;
	VP8StatusCode st = WebPGetFeatures(data, len, &f);
	if (st != VP8_STATUS_OK) return NULL;
	*w = f.width;
	*h = f.height;
	*has_animation = f.has_animation;
	if (f.has_animation) return NULL; // animated sources rejected, not frame-0 optimized
	if (f.width <= 0 || f.height <= 0) return NULL;
	WebPDecoderConfig cfg;
	if (!WebPInitDecoderConfig(&cfg)) return NULL;
	cfg.output.colorspace = MODE_RGBA;
	VP8StatusCode r = WebPDecode(data, len, &cfg);
	if (r != VP8_STATUS_OK) {
		WebPFreeDecBuffer(&cfg.output);
		return NULL;
	}
	size_t size = (size_t)f.width * (size_t)f.height * 4;
	unsigned char* out = (unsigned char*)malloc(size);
	if (!out) { WebPFreeDecBuffer(&cfg.output); return NULL; }
	memcpy(out, cfg.output.u.RGBA.rgba, size);
	WebPFreeDecBuffer(&cfg.output);
	*out_len = size;
	return out;
}
*/
import "C"

import (
	"fmt"
	"image"
)

// decodeWebPToRGBA decodes a (non-animated) WebP bitstream into an
// image.NRGBA. Errors distinguish animation rejection from decode failure.
func decodeWebPToRGBA(data []byte) (image.Image, error) {
	var w, h, hasAnim C.int
	var outLen C.size_t
	ptr := C.webpDecodeRGBA((*C.uint8_t)(&data[0]), C.size_t(len(data)),
		&w, &h, &outLen, &hasAnim)
	if ptr == nil {
		if hasAnim != 0 {
			return nil, fmt.Errorf("animated webp not supported")
		}
		return nil, fmt.Errorf("webp decode failed")
	}
	defer C.free(unsafePointer(ptr))
	dx, dy := int(w), int(h)
	out := make([]byte, int(outLen))
	copySliceFromC(out, ptr)
	img := image.NewNRGBA(image.Rect(0, 0, dx, dy))
	copy(img.Pix, out)
	return img, nil
}

// DecodeWebPForTest decodes WebP bytes through the native libwebp
// decoder. Test-only export for independent output verification.
func DecodeWebPForTest(data []byte) (image.Image, error) {
	return decodeWebPToRGBA(data)
}
