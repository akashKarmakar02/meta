package httpserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gofiber/fiber/v3"

	"go-meta/imageopt"
	"go-meta/imageproc"
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
	// requests respect context cancellation and otherwise queue (bounded
	// by Fiber's request concurrency). Default 4.
	MaxConcurrent int
}

const defaultMaxConcurrent = 4

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
	mountImage(app, limits, cfg)
	return nil
}

func mountImage(app *fiber.App, limits imageopt.Limits, cfg ImageEndpointConfig) {
	var gate *semaphore
	if cfg.MaxConcurrent > 1 {
		gate = newSemaphore(cfg.MaxConcurrent)
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
		return serveImage(c, r, cfg, gate)
	})
}

// serveImage runs the configured processing pipeline. It never writes a
// partial body: the response is fully materialized before any status is
// sent.
func serveImage(c fiber.Ctx, r imageopt.Request, cfg ImageEndpointConfig, gate *semaphore) error {
	ctx := c.Context()
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

	// Bound concurrent native transformations. Waiting respects
	// cancellation; the gate releases on every exit path including panic
	// via defer inside acquire/release.
	if gate != nil {
		if err := gate.acquire(ctx); err != nil {
			return c.Status(http.StatusServiceUnavailable).SendString("image processing busy, retry later")
		}
		defer gate.release()
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

	// Validate the result before publishing anything (response atomicity).
	if len(res.Data) == 0 || res.Format != selected {
		return c.Status(http.StatusInternalServerError).SendString("image processing failed")
	}
	wantMIME, merr := selected.MIME()
	if merr != nil || res.MIME != wantMIME {
		return c.Status(http.StatusInternalServerError).SendString("image processing failed")
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
