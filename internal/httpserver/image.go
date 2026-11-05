package httpserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"

	"go-meta/imageopt"
	"go-meta/imageproc"
	"go-meta/internal/imagecache"
)

// The image endpoint (F01 contract, F03 processing).
//
// Pipeline: F01 strict parsing → f=auto negotiation → root-confined
// source resolution → decoded-pixel sniff → bounded-concurrency
// Processor.Process → validated Result → complete HTTP image response.
//
// Status mapping (deliberate, never collapsed into 500):
//
//	400 invalid request (F01 validation)
//	404 source identity resolves under an approved root but no file there
//	405 non-GET/HEAD
//	406 Accept excludes every encodable format (f=auto)
//	413 source bytes or decoded pixels exceed limits
//	415 processor cannot decode the source's format
//	500 unexpected processing failure (no internals disclosed)
//	503 no processor configured/backend unavailable/native slots full
//
// The unconfigured endpoint (no resolver/processor wired) keeps the
// truthful 501 from F01/F02.
//
// Cache policy (F03R): optimized-image URLs are unversioned — the same URL
// can return different bytes after a local source changes — so both
// successes and errors carry Cache-Control: no-store. A stronger
// immutable policy belongs to F05's source-fingerprinted cache identity,
// not to a URL contract that cannot promise byte stability. f=auto adds
// Vary: Accept because the representation depends on negotiation.

// ImageEndpointConfig wires processing into the image endpoint. Zero
// values disable processing: requests stay truthful 501s.
type ImageEndpointConfig struct {
	// Resolver maps approved namespaces to filesystem roots. Required
	// for serving real sources.
	Resolver *ImageResolver
	// Processor encodes output formats. Required for serving.
	Processor imageproc.Processor
	// MaxConcurrent bounds simultaneous native transformations; waiting
	// requests queue for at most maxPermitWait before a 503 overload
	// rejection. Default 4.
	MaxConcurrent int
	// MaxOutputBytes caps encoded output size. Larger encoded results are
	// refused with 500 and never published (F04 §23). Default 64 MiB,
	// well above anything a ≤16 MP output at ≤4096×4096 can produce.
	MaxOutputBytes int64
	// Cache optionally stores completed optimized outputs on disk (F05).
	// Nil disables disk caching entirely — no directory is created or
	// touched. Cache failures never fail a request: they degrade to
	// processing. F06 does not add logging or metrics for these failures.
	Cache *imagecache.Cache
}

const (
	defaultMaxConcurrent  = 4
	defaultMaxOutputBytes = 64 << 20

	// maxProcessingTime bounds the total processing budget of one image
	// request (F04 §26): resolve + decode + resize + encode. Generous
	// enough for a 40 MP source through Catmull-Rom into AVIF on slow
	// hardware; short enough that a stuck request cannot pin a permit
	// indefinitely. Native encode calls are not interruptible mid-call;
	// the deadline stops work between stages.
	maxProcessingTime = 60 * time.Second
)

// MountImage reserves the image endpoint without processing (F01/F02
// behavior): valid requests stay 501, invalid ones 400. Register before
// page routes.
func MountImage(app *fiber.App) {
	mountImage(app, imageopt.DefaultLimits(), ImageEndpointConfig{})
}

// MountImageWithLimits validates a server-specific, bounded request
// policy and reserves the endpoint without processing.
func MountImageWithLimits(app *fiber.App, limits imageopt.Limits) error {
	if err := limits.Validate(); err != nil {
		return err
	}
	mountImage(app, limits, ImageEndpointConfig{})
	return nil
}

// MountImageProcessing wires real optimization behind the endpoint. Both
// resolver and processor must be present; a partial configuration fails
// here rather than half-serving at request time.
func MountImageProcessing(app *fiber.App, limits imageopt.Limits, cfg ImageEndpointConfig) error {
	if err := limits.Validate(); err != nil {
		return err
	}
	if cfg.Resolver == nil || cfg.Processor == nil {
		return errors.New("httpserver: image processing needs both resolver and processor")
	}
	if cfg.MaxConcurrent < 1 {
		cfg.MaxConcurrent = defaultMaxConcurrent
	}
	if cfg.MaxConcurrent > 64 {
		return errors.New("httpserver: image processing concurrency exceeds 64")
	}
	if cfg.MaxOutputBytes < 1 {
		cfg.MaxOutputBytes = defaultMaxOutputBytes
	}
	mountImage(app, limits, cfg)
	return nil
}

func mountImage(app *fiber.App, limits imageopt.Limits, cfg ImageEndpointConfig) {
	var gate, snapshotGate *semaphore
	if cfg.MaxConcurrent >= 1 {
		gate = newSemaphore(cfg.MaxConcurrent)
		// Bound source snapshots independently of expensive transformations.
		// Extra slots allow cache hits to proceed while encoders are busy.
		snapshotGate = newSemaphore(cfg.MaxConcurrent * 2)
	}
	app.All(imageopt.Endpoint, func(c fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "no-store")
		c.Set(fiber.HeaderContentType, "text/plain; charset=utf-8")
		if m := c.Method(); m != http.MethodGet && m != http.MethodHead {
			c.Set(fiber.HeaderAllow, "GET, HEAD")
			return c.Status(http.StatusMethodNotAllowed).SendString("method not allowed")
		}
		r, err := imageopt.ParseRawQuery(string(c.Request().URI().QueryString()))
		if err != nil || r.ValidateWithin(limits) != nil {
			return c.Status(http.StatusBadRequest).SendString("invalid image request")
		}
		if cfg.Resolver == nil || cfg.Processor == nil {
			return c.Status(http.StatusNotImplemented).SendString("image optimization is not implemented")
		}
		return serveImage(c, r, cfg, gate, snapshotGate)
	})
}

// serveImage runs the configured processing pipeline. It never writes a
// partial body: the response is fully materialized before any status is
// sent.
func serveImage(c fiber.Ctx, r imageopt.Request, cfg ImageEndpointConfig, gate, snapshotGate *semaphore) error {
	// Total per-request processing budget (F04 §26): bounds decode,
	// resize, and encode work together. Native encode calls are not
	// interruptible mid-call (documented); the deadline stops work
	// BETWEEN stages, so a canceled request finishes at most the current
	// native call before its output is discarded.
	ctx, cancel := context.WithTimeout(c.Context(), maxProcessingTime)
	defer cancel()
	caps := cfg.Processor.Capabilities()

	// Resolve the concrete output format before touching the filesystem:
	// explicit formats are checked against capabilities; auto negotiates
	// against Accept.
	selected := r.Format
	if r.Format == imageopt.FormatAuto {
		// The representation depends on Accept: shared caches must not mix
		// negotiated variants (F05 cache identity includes the format).
		c.Set(fiber.HeaderVary, "Accept")
		selected = selectFormat(string(c.Request().Header.Peek("Accept")), caps)
		if selected == "" {
			return c.Status(http.StatusNotAcceptable).SendString("no acceptable image format")
		}
	} else if !caps.Encode(r.Format) {
		return c.Status(http.StatusNotAcceptable).SendString("requested format not supported")
	}

	// Every request holds a bounded snapshot slot until its response is ready.
	// This protects memory before source reads, including on cache hits.
	if snapshotGate != nil {
		if err := snapshotGate.acquire(ctx); err != nil {
			if errors.Is(err, errPermitTimeout) {
				return c.Status(http.StatusServiceUnavailable).SendString("image processing busy, retry later")
			}
			return fiber.ErrRequestTimeout
		}
		defer snapshotGate.release()
	}

	// Root-confined source read. The F01 layer already rejected malformed
	// identities; failures here are existence, permission, or size.
	srcBytes, err := cfg.Resolver.Resolve(ctx, r.Source)
	if err != nil {
		return mapResolveError(c, err)
	}
	src := imageproc.Source{URLIdentity: r.Source, Bytes: srcBytes}

	// Sniff decoded dimensions before any pixel allocation: compressed
	// bombs must fail at metadata time. DecodeConfig is the same
	// format-appropriate bounded inspection Process uses, so the endpoint
	// gate and the pipeline gate cannot disagree.
	if sniffer, ok := cfg.Processor.(imageproc.DecodeConfig); ok {
		cfgImg, derr := sniffer.DecodeConfig(src)
		if derr != nil {
			// The processor's inspection already enforces the same
			// ceilings; map its verdicts, not a blanket 415.
			if errors.Is(derr, imageproc.ErrSourceTooLarge) {
				return c.Status(http.StatusRequestEntityTooLarge).SendString("image source too large")
			}
			return c.Status(http.StatusUnsupportedMediaType).SendString("unsupported image source")
		}
		// Invalid dimensions (non-positive axes) are invalid metadata;
		// over-limit dimensions are the 413 resource ceiling. Both are
		// enforced here, before any pixel allocation.
		if perr := cfg.Resolver.limits.CheckDecodedPixels(cfgImg.Width, cfgImg.Height); perr != nil {
			if errors.Is(perr, imageproc.ErrSourceInvalid) {
				return c.Status(http.StatusUnsupportedMediaType).SendString("unsupported image source")
			}
			return c.Status(http.StatusRequestEntityTooLarge).SendString("image source too large")
		}
	}

	// F05 disk-cache lookup. Identity is the canonical F01 CacheIdentity:
	// the request's validated fields plus the negotiated concrete format,
	// the source-content fingerprint of THIS snapshot, and the processor
	// version. A hit serves verified bytes without touching the processor
	// or consuming a native permit. Every pre-processing guarantee above
	// (validation, negotiation, root resolution, byte cap, pixel gate)
	// has already run for hits as well — the cache never bypasses them.
	var cacheKey string
	if cfg.Cache != nil {
		id := imageopt.CacheIdentity{
			Request:          r,
			Namespace:        cfg.Resolver.namespaceFor(r.Source),
			SourceVersion:    sourceVersion(srcBytes),
			ProcessorVersion: processorVersion(cfg.Processor),
			SelectedFormat:   selected,
		}
		key, kerr := id.Key()
		if kerr == nil {
			cacheKey = key
			if cached, gerr := cfg.Cache.Get(ctx, key, selected, cfg.MaxOutputBytes); gerr == nil {
				if err := ctx.Err(); err != nil {
					return mapProcessError(c, err)
				}
				wantMIME, _ := selected.MIME()
				c.Set(fiber.HeaderContentType, wantMIME)
				c.Set(fiber.HeaderContentLength, strconv.Itoa(len(cached)))
				if c.Method() == http.MethodHead {
					return c.Send(nil)
				}
				return c.Send(cached)
			}
			// Any Get failure — miss, corruption removed, closed cache,
			// cancellation while waiting — falls through to processing.
		}
	}

	// Bound concurrent heavy transformations. The permit is taken BEFORE
	// the full decode and resize work (not just around the native
	// encoder), so queued requests cannot allocate large decode buffers
	// while waiting. Waiting is bounded by maxPermitWait; timeout maps to
	// 503 overload, cancellation to the client-gone path.
	if gate != nil {
		if err := gate.acquire(ctx); err != nil {
			if errors.Is(err, errPermitTimeout) {
				return c.Status(http.StatusServiceUnavailable).SendString("image processing busy, retry later")
			}
			return fiber.ErrRequestTimeout // client went away
		}
		defer gate.release()
	}
	// Re-check cancellation after (possibly) queueing: a request whose
	// client disconnected while waiting does no decode work.
	if err := ctx.Err(); err != nil {
		return fiber.ErrRequestTimeout
	}

	transform := imageproc.Transform{
		Width:   r.Width,
		Height:  r.Height,
		Quality: r.Quality,
		Format:  selected,
	}
	res, err := cfg.Processor.Process(ctx, src, transform)
	if err != nil {
		return mapProcessError(c, err)
	}
	// A native encoder can finish after its context is canceled. Discard its
	// result rather than returning or publishing work for an expired request.
	if err := ctx.Err(); err != nil {
		return mapProcessError(c, err)
	}

	// Validate the result before publishing anything (response atomicity).
	if len(res.Data) == 0 || res.Format != selected {
		return c.Status(http.StatusInternalServerError).SendString("image processing failed")
	}
	// Output-size ceiling (F04 §23): an encoder that produced oversized
	// output is a bug or a pathological input; refuse rather than ship
	// megabytes at an unversioned no-store URL. The bytes were already
	// allocated by the encoder — this check refuses publication, it does
	// not claw back memory (documented limitation).
	if int64(len(res.Data)) > cfg.MaxOutputBytes {
		return c.Status(http.StatusInternalServerError).SendString("image processing failed")
	}
	wantMIME, merr := selected.MIME()
	if merr != nil || res.MIME != wantMIME {
		return c.Status(http.StatusInternalServerError).SendString("image processing failed")
	}

	// F05 best-effort publication: a fully validated result is published
	// under the canonical identity so later requests can hit. Publication
	// failure never fails this response unless the request itself expired.
	if cfg.Cache != nil && cacheKey != "" {
		_ = cfg.Cache.Put(ctx, cacheKey, selected, res.Data)
	}
	if err := ctx.Err(); err != nil {
		return mapProcessError(c, err)
	}

	c.Set(fiber.HeaderContentType, wantMIME)
	if c.Method() == http.MethodHead {
		// Perform the transformation to report a truthful Content-Length
		// and Content-Type, then suppress the body — HEAD mirrors GET
		// semantics exactly, minus the bytes.
		c.Set(fiber.HeaderContentLength, strconv.Itoa(len(res.Data)))
		return c.Send(nil)
	}
	c.Set(fiber.HeaderContentLength, strconv.Itoa(len(res.Data)))
	return c.Send(res.Data)
}

// mapResolveError maps resolver failures onto safe statuses without
// disclosing which root, path, or filesystem condition failed.
func mapResolveError(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, errImageSourceNotFound):
		return c.Status(http.StatusNotFound).SendString("image source not found")
	case errors.Is(err, imageproc.ErrSourceTooLarge):
		return c.Status(http.StatusRequestEntityTooLarge).SendString("image source too large")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return fiber.ErrRequestTimeout // client went away; no response to write anyway
	default:
		// Forbidden (symlink escape, special file) fails closed as not
		// found: indistinguishable from missing, no existence oracle.
		return c.Status(http.StatusNotFound).SendString("image source not found")
	}
}

// mapProcessError maps processor failures onto deliberate statuses.
func mapProcessError(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, imageproc.ErrSourceInvalid):
		return c.Status(http.StatusUnsupportedMediaType).SendString("unsupported image source")
	case errors.Is(err, imageproc.ErrSourceTooLarge):
		return c.Status(http.StatusRequestEntityTooLarge).SendString("image source too large")
	case errors.Is(err, imageproc.ErrUnsupportedFormat):
		return c.Status(http.StatusNotAcceptable).SendString("requested format not supported")
	case errors.Is(err, imageproc.ErrInvalidTransform):
		return c.Status(fiber.StatusBadRequest).SendString("invalid image request")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return fiber.ErrRequestTimeout
	default:
		return c.Status(http.StatusInternalServerError).SendString("image processing failed")
	}
}
