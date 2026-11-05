//go:build !imageproc_native

// Package imageprocessor without the imageproc_native build tag: the
// native CGO backend is not compiled, so the only processor this package
// can construct reports no capabilities and fails closed. This keeps the
// default build dependency-free while the public contract (imageopt,
// imageproc) remains fully available; see the F02 decision record in
// README.md.
package imageprocessor

import (
	"context"
	"fmt"

	"github.com/akashKarmakar02/meta/imageopt"
	"github.com/akashKarmakar02/meta/imageproc"
)

// Native satisfies imageproc.Processor in untagged builds, but every
// operation fails closed: New reports the backend unavailable with an
// actionable cause, capabilities are empty, and Process never fabricates
// output.
type Native struct{}

var _ imageproc.Processor = Native{}

// New reports ErrBackendUnavailable: build with -tags imageproc_native and
// the native libraries installed (see README "Image processor backend").
func New(context.Context) (Native, error) {
	return Native{}, fmt.Errorf("%w: built without imageproc_native", imageproc.ErrBackendUnavailable)
}

// Capabilities is empty in untagged builds.
func (Native) Capabilities() imageproc.Capabilities { return imageproc.Capabilities{} }

// Process always fails closed; no image bytes are ever produced.
func (Native) Process(context.Context, imageproc.Source, imageproc.Transform) (imageproc.Result, error) {
	return imageproc.Result{}, fmt.Errorf("image processor disabled: %w", imageproc.ErrBackendUnavailable)
}

// Info names the disabled state.
func (Native) Info() imageproc.BackendInfo {
	return imageproc.BackendInfo{Name: "disabled (build without imageproc_native)"}
}

// Version is a stable marker for this build shape.
func (Native) Version() string { return "native-disabled" }

// Close is a safe no-op.
func (Native) Close() error { return nil }

var _ = imageopt.FormatAuto
