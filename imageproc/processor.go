// Package imageproc defines the F02 image-processing abstraction: a
// Processor turns validated F01 requests plus trusted, resolved source
// bytes into owned encoded output. The concrete native backend lives in
// internal/imageprocessor behind this interface; imageopt stays free of
// native dependencies and this package imports no HTTP, Fiber, templ, or
// application-route code.
package imageproc

import (
	"context"
	"errors"
	"fmt"
	"image"
	"sync"

	"github.com/akashKarmakar02/meta/imageopt"
)

// Sentinel errors. Internal causes remain wrapped for server-side
// diagnostics; the HTTP layer never forwards them to clients.
var (
	// ErrUnsupportedFormat reports that the configured processor cannot
	// encode (or, in future, decode) the requested format.
	ErrUnsupportedFormat = errors.New("imageproc: unsupported image format")
	// ErrInvalidTransform reports a transformation that is structurally
	// valid as a request but unusable by the processor.
	ErrInvalidTransform = errors.New("imageproc: invalid transformation")
	// ErrProcessing reports that processing was attempted and failed.
	ErrProcessing = errors.New("imageproc: processing failed")
)

// Source is a fully trusted, already-loaded image: bytes produced by an
// approved-root resolver (F01 defines that boundary), plus the URL
// identity it was loaded from. The processor performs no filesystem
// access: a raw src query string never reaches this type, and any path
// disclosure is the resolver's responsibility. Bytes are owned by the
// caller and treated as read-only by implementations.
type Source struct {
	// URLIdentity is the validated /images/... or /assets/... identity.
	URLIdentity string
	// Bytes is the encoded source image.
	Bytes []byte
}

// Transform is the validated transformation derived from an F01 request.
// Format must be concrete; FormatAuto is negotiated above the processor.
type Transform struct {
	Width   int
	Height  int
	Quality int // 1–100
	Format  imageopt.Format
}

// Result carries a successful transformation. Data is exclusively owned
// by the caller on return; implementations must not retain it or hand
// the same buffer to two results. Format and MIME always agree.
type Result struct {
	Data    []byte
	Format  imageopt.Format
	MIME    string
	Width   int
	Height  int
	Version string // processor identity that produced this output
}

// Processor executes one image transformation at a time. Implementations
// must be safe for concurrent Process calls; concurrency limiting is
// added at the integration boundary (F04), so this interface must remain
// callable without a global lock.
type Processor interface {
	Capabilities() Capabilities
	Process(ctx context.Context, src Source, t Transform) (Result, error)
}

// Capabilities reports actually-detected decode/encode support of the
// underlying backend at initialization time. Immutable after the
// processor is constructed.
type Capabilities struct {
	JPEGDecode bool
	PNGDecode  bool
	WebPDecode bool
	AVIFDecode bool

	JPEGEncode bool
	PNGEncode  bool
	WebPEncode bool
	AVIFEncode bool
}

// Encode reports whether format has an encoder.
func (c Capabilities) Encode(f imageopt.Format) bool {
	switch f {
	case imageopt.FormatAVIF:
		return c.AVIFEncode
	case imageopt.FormatWebP:
		return c.WebPEncode
	case imageopt.FormatJPEG:
		return c.JPEGEncode
	case imageopt.FormatPNG:
		return c.PNGEncode
	default:
		return false
	}
}

// BackendInfo names the selected backend and its native library
// versions, for the decision record and diagnostics.
type BackendInfo struct {
	Name         string
	LibraryInfo  []string // e.g. "libwebp 1.6.0", "libavif 1.3.0"
	PipelineNote string
}

// New builds the production processor. It validates the transform
// contract early, rejects FormatAuto at construction-independent call
// sites, and returns wrapped ErrBackendUnavailable when native
// dependencies are missing. Version identity is captured once and stays
// stable for the process lifetime.
func New(ctx context.Context) (Processor, error) {
	p, err := newBackend(ctx)
	if err != nil {
		return nil, fmt.Errorf("imageproc: %w", err)
	}
	return p, nil
}

// ValidateTransform checks a transformation against hard request rules
// without touching capabilities (format support is separate).
func ValidateTransform(t Transform) error {
	if t.Quality < 1 || t.Quality > 100 {
		return ErrInvalidTransform
	}
	if t.Width < 0 || t.Height < 0 || t.Width > imageopt.MaxDimension || t.Height > imageopt.MaxDimension {
		return ErrInvalidTransform
	}
	if t.Width == 0 && t.Height == 0 {
		return ErrInvalidTransform
	}
	if int64(t.Width)*int64(t.Height) > imageopt.MaxPixels {
		return ErrInvalidTransform
	}
	switch t.Format {
	case imageopt.FormatAVIF, imageopt.FormatWebP, imageopt.FormatJPEG, imageopt.FormatPNG:
	default:
		return fmt.Errorf("%w: %q", ErrInvalidTransform, string(t.Format))
	}
	return nil
}

// PreProcess enforces what every processor must guarantee before any
// backend runs: cancellation checked first, then contract validation,
// then capability checks. Backends call this from Process.
func PreProcess(ctx context.Context, caps Capabilities, t Transform) error {
	if ctx == nil {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateTransform(t); err != nil {
		return err
	}
	if !caps.Encode(t.Format) {
		return fmt.Errorf("%w: encode %s", ErrUnsupportedFormat, string(t.Format))
	}
	return nil
}

// DecodeConfig is implemented by backends to sniff dimensions without a
// full decode (F05 cache identity can use it later).
type DecodeConfig interface {
	DecodeConfig(src Source) (image.Config, error)
}

// Close is optional per processor; the native group supplies it.
var _ interface{ Close() } = (*noopClose)(nil)

type noopClose struct{}

func (noopClose) Close() {}

// Group lets one processor serve multiple formats while keeping
// capability reporting unified; retained for F03's dispatch layer.
var _ Processor = (Processor(nil))

// sync.Once guards version identity computation in backends.
var _ = sync.Once{}
