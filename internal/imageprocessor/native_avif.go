//go:build imageproc_native

package imageprocessor

/*
#cgo pkg-config: libavif
#include <stdlib.h>
#include <string.h>
#include <avif/avif.h>

// encodeAVIFRGBA encodes packed RGBA rows into AVIF. quality is the go-meta
// 1-100 scale (higher is better); it maps onto libavif's quantizer range
// (0 lossless .. 63 floor) before encoding. maxThreads bounds the encoder
// thread pool so F04's concurrency limits stay enforceable.
static unsigned char* encodeAVIFRGBA(const uint8_t* rgba, int width, int height,
	int quality, int speed, int maxThreads, size_t* out_len, int* err_code) {
	avifImage* img = avifImageCreate(width, height, 8, AVIF_PIXEL_FORMAT_YUV420);
	if (!img) { *err_code = (int)AVIF_RESULT_OUT_OF_MEMORY; return NULL; }
	avifResult r = avifImageAllocatePlanes(img, AVIF_PLANES_YUV);
	if (r != AVIF_RESULT_OK) { *err_code = (int)r; avifImageDestroy(img); return NULL; }
	avifRGBImage rgb;
	memset(&rgb, 0, sizeof(rgb));
	avifRGBImageSetDefaults(&rgb, img);
	rgb.pixels = (uint8_t*)rgba;
	rgb.rowBytes = (uint32_t)(width * 4);
	r = avifImageRGBToYUV(img, &rgb);
	if (r != AVIF_RESULT_OK) { *err_code = (int)r; avifImageDestroy(img); return NULL; }

	avifEncoder* enc = avifEncoderCreate();
	if (!enc) { *err_code = (int)AVIF_RESULT_OUT_OF_MEMORY; avifImageDestroy(img); return NULL; }
	int iq = (100 - quality) * 63 / 99;
	enc->minQuantizer = iq;
	enc->maxQuantizer = iq + 10;
	if (enc->maxQuantizer > 63) enc->maxQuantizer = 63;
	if (enc->minQuantizer > enc->maxQuantizer) enc->minQuantizer = enc->maxQuantizer;
	enc->speed = speed;
	enc->maxThreads = maxThreads;
	r = avifEncoderAddImage(enc, img, 1, 1);
	if (r != AVIF_RESULT_OK) { *err_code = (int)r; avifEncoderDestroy(enc); avifImageDestroy(img); return NULL; }
	avifRWData out = AVIF_DATA_EMPTY;
	r = avifEncoderFinish(enc, &out);
	avifEncoderDestroy(enc);
	avifImageDestroy(img);
	if (r != AVIF_RESULT_OK) {
		// F06 §24: finish may allocate into out before failing; free it
		// unconditionally on the error path (avifRWDataFree is a documented
		// no-op on an empty/zeroed struct).
		avifRWDataFree(&out);
		*err_code = (int)r;
		return NULL;
	}
	*out_len = out.size;
	return out.data; // ownership moves to caller
}

static void freeAVIFOutput(uint8_t* data, size_t size) {
	avifRWData output = { data, size };
	avifRWDataFree(&output);
}
*/
import "C"

import (
	"fmt"
	"image"
)

// encodeAVIFC wraps the C helper with Go-owned buffers throughout.
func encodeAVIFC(img image.Image, quality int) ([]byte, error) {
	b := img.Bounds()
	dx, dy := b.Dx(), b.Dy()
	rgba := toRGBA(img)
	q := quality
	if q < 1 {
		q = 1
	}
	var outLen C.size_t
	var errCode C.int
	ptr := C.encodeAVIFRGBA(
		(*C.uint8_t)(&rgba[0]),
		C.int(dx), C.int(dy), C.int(q),
		C.int(6), // speed 6: balanced default; F03 can tune per format limits
		C.int(2), // bounded encoder threads
		&outLen, &errCode,
	)
	if ptr == nil {
		return nil, fmt.Errorf("libavif encode failed: avifResult %d", int(errCode))
	}
	defer C.freeAVIFOutput(ptr, outLen)
	out := make([]byte, int(outLen))
	copySliceFromC(out, ptr)
	return out, nil
}

// avifEncodeProbe encodes a 1×1 RGBA pixel once; New uses it to prove the
// linked libavif has a working AV1 codec, not merely loadable symbols
// (F04 §31 — no false capability from shared-library presence alone).
func avifEncodeProbe() error {
	px := []byte{0x10, 0x20, 0x30, 0xff}
	var outLen C.size_t
	var errCode C.int
	ptr := C.encodeAVIFRGBA((*C.uint8_t)(&px[0]), 1, 1, 50, 9, 1, &outLen, &errCode)
	if ptr == nil {
		return fmt.Errorf("libavif probe encode failed: avifResult %d", int(errCode))
	}
	C.freeAVIFOutput(ptr, outLen)
	return nil
}
