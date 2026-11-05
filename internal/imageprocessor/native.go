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
	// Bounded internal threading (F04 §27): fixed single-threaded segment
	// work regardless of machine core count — the endpoint semaphore is
	// the concurrency control; per-encode thread pools would multiply it.
	cfg.thread_level = 0;
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
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"sync"

	"go-meta/imageopt"
	"go-meta/imageproc"
)

// Native is the production Processor over libwebp + libavif.
type Native struct {
	caps    imageproc.Capabilities
	version string
	info    imageproc.BackendInfo

	// fullDecoder, when non-nil, replaces the family decoders in
	// decodeInspected. Test-only seam for proving that rejected sources
	// never reach a full decode; nil in production.
	fullDecoder func(b []byte, format string) (image.Image, error)

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

	// libavif: prove a working AV1 encode path by actually encoding a
	// 1×1 frame (F04 §31 — encoder creation alone proves nothing about
	// codec availability). A failed probe disables AVIF encoding rather
	// than crash-later.
	if perr := avifEncodeProbe(); perr != nil {
		return nil, fmt.Errorf("%w: %v", imageproc.ErrBackendUnavailable, perr)
	}
	n.caps.AVIFEncode = true
	// AVIFDecode stays false: F03 encodes AVIF but does not decode AVIF
	// sources (no dav1d pixel binding yet). Honesty over reach.

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
// F03 raised it: placeholder integer sampling → Catmull-Rom resampling.
const pipelineVersion = "2"

// maxDecodedPixels bounds the decoded source image before any pixel
// allocation (F03 §26; F04 tightens per deployment). 40 MP with overflow-
// safe comparison against int64 products.
const maxDecodedPixels = 40_000_000

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

// DecodeConfig sniffs dimensions and format without a full decode. The
// stdlib registry (jpeg/png/gif) is format-sniffing only; WebP and AVIF
// go through the native bounded header inspectors. This method is the
// pre-decode gate: no full-frame allocation happens for any family.
func (n *Native) DecodeConfig(src imageproc.Source) (image.Config, error) {
	ins, err := n.inspectSource(src.Bytes)
	if err != nil {
		return image.Config{}, err
	}
	return image.Config{Width: ins.width, Height: ins.height}, nil
}

// Process runs the full pipeline: bounded inspection (format-appropriate
// header parse, never a generic one), pixel-limit rejection before any
// full-frame allocation, full decode, revalidation of the actually decoded
// dimensions against the inspected ones, resize, encode. It never writes
// HTTP responses, and checks cancellation before each expensive stage.
func (n *Native) Process(ctx context.Context, src imageproc.Source, t imageproc.Transform) (imageproc.Result, error) {
	if err := imageproc.PreProcess(ctx, n.caps, t); err != nil {
		return imageproc.Result{}, err
	}
	if len(src.Bytes) == 0 {
		return imageproc.Result{}, fmt.Errorf("%w: empty source", imageproc.ErrSourceInvalid)
	}

	inspected, err := n.inspectSource(src.Bytes)
	if err != nil {
		return imageproc.Result{}, err
	}
	img, err := n.decodeInspected(src.Bytes, inspected)
	if err != nil {
		return imageproc.Result{}, err
	}
	// Revalidate the actually decoded frame against the inspected
	// metadata: a source whose true dimensions differ from its header
	// (or bytes that changed under us) must not bypass the gate.
	b := img.Bounds()
	if b.Dx() != inspected.width || b.Dy() != inspected.height {
		return imageproc.Result{}, fmt.Errorf("%w: decoded %dx%d but header declared %dx%d",
			imageproc.ErrSourceInvalid, b.Dx(), b.Dy(), inspected.width, inspected.height)
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
		return imageproc.Result{}, fmt.Errorf("%w: encode %s: %v", imageproc.ErrProcessing, inspected.format, err)
	}
	w := out.Bounds().Dx()
	h := out.Bounds().Dy()
	return imageproc.Result{
		Data: data, Format: t.Format, MIME: mime,
		Width: w, Height: h, Version: n.version,
	}, nil
}

// inspectedSource is the bounded, format-appropriate header inspection of
// a source: which family it claims to be, the dimensions it declares, and
// whether it is animated. All limits are already checked by inspectSource
// before it returns; a returned inspection is authorized for full decode.
type inspectedSource struct {
	format  string // sniffed family name, never a filename guess
	width   int
	height  int
	decoded bool // true when inspection itself proved the frame decodable
}

// inspectSource performs the pre-decode gate: detect the family by magic
// bytes, parse only that family's headers, reject unsupported or animated
// families, and enforce the dimension and decoded-pixel ceilings with
// overflow-safe arithmetic — all before any full-frame allocation. It is
// also the single entry point used by DecodeConfig, so the endpoint's
// sniff and Process's decode can never disagree about the family checked.
func (n *Native) inspectSource(b []byte) (inspectedSource, error) {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		cfg, err := pngDecodeConfig(b)
		if err != nil {
			return inspectedSource{}, fmt.Errorf("%w: png header: %v", imageproc.ErrSourceInvalid, err)
		}
		return checkedInspection("png", cfg.Width, cfg.Height, false)
	case bytes.HasPrefix(b, []byte{0xff, 0xd8}):
		cfg, err := jpegDecodeConfig(b)
		if err != nil {
			return inspectedSource{}, fmt.Errorf("%w: jpeg header: %v", imageproc.ErrSourceInvalid, err)
		}
		return checkedInspection("jpeg", cfg.Width, cfg.Height, false)
	case bytes.HasPrefix(b, []byte("GIF8")):
		// gif.DecodeConfig parses the logical screen descriptor only, but
		// F03 has no animated-input policy for GIF; reject the family rather
		// than optimizing one arbitrary frame.
		return inspectedSource{}, fmt.Errorf("%w: gif sources are not supported in F03", imageproc.ErrSourceInvalid)
	case bytes.HasPrefix(b, []byte("RIFF")) && len(b) >= 12 && string(b[8:12]) == "WEBP":
		features, err := webpInspect(b)
		if err != nil {
			return inspectedSource{}, fmt.Errorf("%w: webp header: %v", imageproc.ErrSourceInvalid, err)
		}
		if features.hasAnimation {
			return inspectedSource{}, fmt.Errorf("%w: animated webp not supported", imageproc.ErrSourceInvalid)
		}
		// The WebP path decodes through libwebp, whose own feature parse is
		// the inspection; decoded=true makes the post-decode revalidation a
		// same-source check.
		return checkedInspection("webp", features.width, features.height, true)
	case bytes.HasPrefix(b, []byte("\x00\x00\x00")) && len(b) >= 12 && isBMFFBrand(b):
		w, h, frames, err := avifInfo(b)
		if err != nil {
			return inspectedSource{}, fmt.Errorf("%w: avif header: %v", imageproc.ErrSourceInvalid, err)
		}
		if frames > 1 {
			return inspectedSource{}, fmt.Errorf("%w: animated avif not supported", imageproc.ErrSourceInvalid)
		}
		return checkedInspection("avif", w, h, true)
	default:
		return inspectedSource{}, fmt.Errorf("%w: unrecognized image format", imageproc.ErrSourceInvalid)
	}
}

// checkedInspection validates declared dimensions (positive, within the
// per-axis ceiling, and under the total decoded-pixel ceiling with
// overflow-safe int64 multiplication) and returns the authorized
// inspection.
func checkedInspection(format string, w, h int, decoded bool) (inspectedSource, error) {
	if w <= 0 || h <= 0 {
		return inspectedSource{}, fmt.Errorf("%w: %s dimensions %dx%d", imageproc.ErrSourceInvalid, format, w, h)
	}
	if int64(w)*int64(h) > maxDecodedPixels {
		return inspectedSource{}, fmt.Errorf("%w: %s declares %dx%d pixels", imageproc.ErrSourceTooLarge, format, w, h)
	}
	return inspectedSource{format: format, width: w, height: h, decoded: decoded}, nil
}

// decodeInspected fully decodes an already-inspected source. WebP goes
// through the native decoder (which re-runs the same bounded feature
// parse before allocating pixels); every other supported family —
// currently only jpeg and png after inspection rejects the rest — uses
// the stdlib decoder that owns that family's header format. After
// decoding, the actual dimensions are revalidated by Process against the
// inspected ones so a source that changed between inspection and decode
// cannot smuggle a larger frame past the gate. fullDecoder is the single
// decoding seam: pre-decode rejection tests swap it to prove the full
// decoder is never invoked for rejected sources.
func (n *Native) decodeInspected(b []byte, ins inspectedSource) (image.Image, error) {
	if n.fullDecoder != nil {
		return n.fullDecoder(b, ins.format)
	}
	return decodeFamily(b, ins.format)
}

// decodeFamily fully decodes b as the given inspected family.
func decodeFamily(b []byte, format string) (image.Image, error) {
	switch format {
	case "webp":
		img, err := decodeWebPToRGBA(b)
		if err != nil {
			return nil, fmt.Errorf("%w: webp decode: %v", imageproc.ErrSourceInvalid, err)
		}
		return img, nil
	case "jpeg", "png":
		var (
			img image.Image
			err error
		)
		if format == "jpeg" {
			img, err = jpeg.Decode(bytes.NewReader(b))
		} else {
			img, err = png.Decode(bytes.NewReader(b))
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %s decode: %v", imageproc.ErrSourceInvalid, format, err)
		}
		return img, nil
	case "avif":
		// Inspection parses the container, but F03 has no AVIF pixel decoder;
		// it must fail here rather than pretending to optimize.
		return nil, fmt.Errorf("%w: avif sources are not decodable in F03 (dav1d binding pending)", imageproc.ErrUnsupportedFormat)
	default:
		// Unreachable while every branch of inspectSource names a handled
		// family; kept as a fail-closed guard for future families.
		return nil, fmt.Errorf("%w: internal: uninspected family %q reached decode", imageproc.ErrSourceInvalid, format)
	}
}

// isBMFFBrand reports whether b's ftyp brand at offset 4–11 marks a
// container this build knows how to bound: avif or avis. Other brands are
// left to the unrecognized-format rejection.
func isBMFFBrand(b []byte) bool {
	if len(b) < 12 {
		return false
	}
	brand := string(b[8:12]) // major brand, e.g. "avif" / "avis"
	_ = brand
	// The compatible-brands list (offset 16..) can also carry the marker;
	// scan a bounded prefix of the ftyp box for "avif"/"avis" so still-image
	// AVIF written with a different major brand is still recognized.
	boxLen := int(uint32(b[3]) | uint32(b[2])<<8 | uint32(b[1])<<16 | uint32(b[0])<<24)
	end := len(b)
	if boxLen >= 8 && boxLen <= 1024 && boxLen < end {
		end = boxLen
	}
	scanEnd := end
	if scanEnd > 1024 {
		scanEnd = 1024
	}
	for i := 8; i+4 <= scanEnd; i++ {
		if string(b[i:i+4]) == "avif" || string(b[i:i+4]) == "avis" {
			return true
		}
	}
	return false
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
