//go:build imageproc_native

package imageprocessor

/*
#cgo pkg-config: libavif
#include <stdlib.h>
#include <avif/avif.h>

// avifSniff decodes only the header metadata of an AVIF bitstream:
// dimensions, depth, and frame count (animation). This is libavif's own
// bounded parse — no pixel allocation happens. Returns an avifResult
// code; 0 = OK.
static int avifSniff(const uint8_t* data, size_t len,
	int* w, int* h, int* frame_count) {
	avifDecoder* dec = avifDecoderCreate();
	if (!dec) return (int)AVIF_RESULT_OUT_OF_MEMORY;
	dec->maxThreads = 1;
	avifResult r = avifDecoderSetIOMemory(dec, data, len);
	if (r == AVIF_RESULT_OK) r = avifDecoderParse(dec);
	if (r == AVIF_RESULT_OK) {
		*w = dec->image->width;
		*h = dec->image->height;
		*frame_count = dec->imageCount;
	}
	avifDecoderDestroy(dec);
	return (int)r;
}
*/
import "C"

import "fmt"

// avifInfo sniffs dimensions and frame count of an AVIF bitstream.
// frameCount > 1 means the bitstream is animated.
func avifInfo(data []byte) (w, h, frameCount int, err error) {
	var cw, ch, cf C.int
	r := C.avifSniff((*C.uint8_t)(&data[0]), C.size_t(len(data)), &cw, &ch, &cf)
	if r != 0 {
		return 0, 0, 0, fmt.Errorf("avif sniff: avifResult %d", int(r))
	}
	return int(cw), int(ch), int(cf), nil
}
