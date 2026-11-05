//go:build imageproc_native

// Package imageprocessor implements imageproc.Processor against the
// system native image libraries through a small first-party CGO binding:
// libwebp and libavif for encoding, Go's standard library for JPEG/PNG
// decode and encode. All cgo code lives in this internal package behind
// the imageproc_native build tag; no public go-meta package exposes C
// types, and default builds compile none of it.
package imageprocessor

/*
#cgo pkg-config: libwebp libavif
#include <stdlib.h>
#include <string.h>
#include <webp/encode.h>
#include <webp/decode.h>
#include <avif/avif.h>

// ---- WebP ----

// encodeWebP encodes packed RGBA rows into lossy WebP at the given
// quality (0-100). Returns a malloc'd buffer the caller frees; *out_len
// receives its size. Returns NULL on failure.
static unsigned char* encodeWebP(const uint8_t* rgba, int width, int height, float quality, size_t* out_len) {
	WebPPicture pic;
	if (!WebPPictureInit(&pic)) return NULL;
	pic.width = width;
	pic.height = height;
	pic.use_argb = 1;
	if (!WebPPictureImportRGBA(&pic, rgba, width * 4)) {
		WebPPictureFree(&pic);
		return NULL;
	}
	WebPConfig cfg;
	if (!WebPConfigPreset(&cfg, WEBP_PRESET_DEFAULT, quality)) {
		WebPPictureFree(&pic);
		return NULL;
	}
	WebPMemoryWriter writer;
	WebPMemoryWriterInit(&writer);
	pic.writer = WebPMemoryWrite;
	pic.custom_ptr = &writer;
	int ok = WebPEncode(&cfg, &pic);
	WebPPictureFree(&pic);
	if (!ok) {
		WebPMemoryWriterClear(&writer);
		return NULL;
	}
	*out_len = writer.size;
	return writer.mem; // ownership moves to caller; do not clear the writer
}

// webpEncoderVersion returns the linked encoder version, e.g. 0x010600.
static int webpEncoderVersion(void) { return WebPGetEncoderVersion(); }

// webpDecodeInfo decodes only the dimensions/format of a WebP bitstream.
static int webpDecodeInfo(const uint8_t* data, size_t len, int* w, int* h) {
	WebPBitstreamFeatures f;
	VP8StatusCode st = WebPGetFeatures(data, len, &f);
	if (st != VP8_STATUS_OK) return -1;
	*w = f.width;
	*h = f.height;
	return 0;
}
*/
import "C"

import (
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"sync"

	"go-meta/imageopt"
	"go-meta/imageproc"
)

// Native is the production Processor over libwebp + libavif.
type Native struct {
	caps    imageproc.Capabilities
	version string
	info    imageproc.BackendInfo

	webpMu sync.Mutex // serializes the libwebp per-call encoder state if
	// a future change introduces global encoder settings;
	// current calls are independent and lock-free.
}

var (
	_ imageproc.Processor    = (*Native)(nil)
	_ imageproc.DecodeConfig = (*Native)(nil)
)

// New probes the native libraries once and returns the processor with its
// immutable capabilities and version identity, or ErrBackendUnavailable
// with the concrete cause when a required library cannot be used.
func New(ctx context.Context) (*Native, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	n := &Native{}
	ver := int(C.webpEncoderVersion())
	if ver == 0 {
		return nil, fmt.Errorf("%w: libwebp reports encoder version 0", imageproc.ErrBackendUnavailable)
	}
	n.caps.WebPEncode = true
	n.caps.WebPDecode = true // same library provides WebPGetFeatures

	// libavif: prove the encoder path by creating and destroying an
	// encoder, the cheapest reliable probe of a usable codec build.
	enc := C.avifEncoderCreate()
	if enc == nil {
		return nil, fmt.Errorf("%w: libavif encoder creation failed", imageproc.ErrBackendUnavailable)
	}
	C.avifEncoderDestroy(enc)
	n.caps.AVIFEncode = true
	n.caps.AVIFDecode = true // dav1d is a hard dependency of Fedora's libavif

	// JPEG/PNG via the Go standard library.
	n.caps.JPEGDecode = true
	n.caps.PNGDecode = true
	n.caps.PNGEncode = true
	n.caps.JPEGEncode = true

	n.version = fmt.Sprintf("native-webp:%06x-avif:%d.%d.%d-pipeline:%s",
		ver, C.AVIF_VERSION_MAJOR, C.AVIF_VERSION_MINOR, C.AVIF_VERSION_PATCH, pipelineVersion)
	n.info = imageproc.BackendInfo{
		Name: "libwebp+libavif (first-party CGO)",
		LibraryInfo: []string{
			fmt.Sprintf("libwebp encoder %d.%d.%d", ver>>16, (ver>>8)&0xff, ver&0xff),
			fmt.Sprintf("libavif %d.%d.%d", C.AVIF_VERSION_MAJOR, C.AVIF_VERSION_MINOR, C.AVIF_VERSION_PATCH),
		},
		PipelineNote: "Go stdlib jpeg/png decode+encode; resize in Go; native WebP/AVIF encode",
	}
	return n, nil
}

// pipelineVersion is bumped when the processing pipeline behavior (resize
// algorithm, color handling, encoder mapping) changes in a way that would
// invalidate cached optimized outputs (F05 folds it into cache identity).
const pipelineVersion = "1"

// Capabilities is immutable after New.
func (n *Native) Capabilities() imageproc.Capabilities { return n.caps }

// Info returns backend identity for diagnostics.
func (n *Native) Info() imageproc.BackendInfo { return n.info }

// Version returns the stable processor identity for this process.
func (n *Native) Version() string { return n.version }

// Close is a safe no-op: this backend holds no native heap allocations
// between Process calls, so there is nothing to release or leak. Safe to
// call during active Process calls (they only borrow process-global
// library state that outlives every processor).
func (n *Native) Close() error { return nil }

// DecodeConfig sniffs dimensions without a full decode.
func (n *Native) DecodeConfig(src imageproc.Source) (image.Config, error) {
	cfg, _, err := image.DecodeConfig(bytesReader(src.Bytes))
	return cfg, err
}

// Process runs the full pipeline: decode, resize, encode. It never writes
// HTTP responses, and checks cancellation before each expensive stage.
func (n *Native) Process(ctx context.Context, src imageproc.Source, t imageproc.Transform) (imageproc.Result, error) {
	if err := imageproc.PreProcess(ctx, n.caps, t); err != nil {
		return imageproc.Result{}, err
	}
	if len(src.Bytes) == 0 {
		return imageproc.Result{}, fmt.Errorf("%w: empty source", imageproc.ErrSourceInvalid)
	}

	img, formatName, err := image.Decode(bytesReader(src.Bytes))
	if err != nil {
		return imageproc.Result{}, fmt.Errorf("%w: decode: %v", imageproc.ErrSourceInvalid, err)
	}
	if err := ctx.Err(); err != nil {
		return imageproc.Result{}, err
	}

	out := resizeToFit(img, t.Width, t.Height)

	mime, err := t.Format.MIME()
	if err != nil {
		return imageproc.Result{}, err
	}
	var data []byte
	switch t.Format {
	case imageopt.FormatWebP:
		data, err = n.encodeWebP(out, t.Quality)
	case imageopt.FormatAVIF:
		data, err = encodeAVIFC(out, t.Quality)
	case imageopt.FormatJPEG:
		data, err = encodeJPEG(out, t.Quality)
	case imageopt.FormatPNG:
		data, err = encodePNG(out)
	default:
		return imageproc.Result{}, fmt.Errorf("%w: %q", imageproc.ErrUnsupportedFormat, string(t.Format))
	}
	if err != nil {
		return imageproc.Result{}, fmt.Errorf("%w: encode %s: %v", imageproc.ErrProcessing, formatName, err)
	}
	w := out.Bounds().Dx()
	h := out.Bounds().Dy()
	return imageproc.Result{
		Data: data, Format: t.Format, MIME: mime,
		Width: w, Height: h, Version: n.version,
	}, nil
}

func (n *Native) encodeWebP(img image.Image, quality int) ([]byte, error) {
	b := img.Bounds()
	dx, dy := b.Dx(), b.Dy()
	rgba := toRGBA(img)
	if quality < 1 {
		quality = 1
	}
	q := C.float(quality)
	var outLen C.size_t
	ptr := C.encodeWebP((*C.uint8_t)(&rgba[0]), C.int(dx), C.int(dy), q, &outLen)
	if ptr == nil {
		return nil, fmt.Errorf("libwebp encode failed")
	}
	defer C.free(unsafePointer(ptr))
	size := int(outLen)
	out := make([]byte, size)
	copySliceFromC(out, ptr)
	return out, nil
}
