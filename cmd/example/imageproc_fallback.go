//go:build !imageproc_native

// F03 wiring for untagged builds: the native backend is not compiled, so
// the endpoint stays in its truthful 501 configuration from F01/F02. The
// server starts and serves normally — no startup failure, no fabricated
// image bytes.
package main

import (
	"context"

	"github.com/gofiber/fiber/v3"

	"github.com/akashKarmakar02/meta/imageproc"
	"github.com/akashKarmakar02/meta/internal/httpserver"
)

func mountImage(fapp *fiber.App) {
	// Untagged builds keep the F01/F02 truthful reserved endpoint:
	// valid → 501, invalid → 400. No processor is constructed.
	httpserver.MountImage(fapp)
}

// defaultImageProcessingAvailable: no processor exists in untagged builds.
func defaultImageProcessingAvailable() bool { return false }

// newImageProcessor always fails closed in untagged builds.
func newImageProcessor(ctx context.Context) (imageproc.Processor, error) {
	return nil, imageproc.ErrBackendUnavailable
}
