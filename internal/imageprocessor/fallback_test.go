//go:build !imageproc_native

package imageprocessor

import (
	"context"
	"errors"
	"testing"

	"github.com/akashKarmakar02/meta/imageopt"
	"github.com/akashKarmakar02/meta/imageproc"
)

// TestFallbackBuildFailsClosed: without the build tag, New reports
// ErrBackendUnavailable with an actionable cause rather than pretending a
// backend exists.
func TestFallbackBuildFailsClosed(t *testing.T) {
	p, err := New(context.Background())
	_ = p // always the zero value in untagged builds; err is the contract
	if err == nil {
		t.Fatal("untagged build returned a working backend")
	}
	if !errors.Is(err, imageproc.ErrBackendUnavailable) {
		t.Fatalf("error: %v", err)
	}
	var nop imageproc.Processor = imageproc.NoopProcessor{}
	if _, err := nop.Process(context.Background(), imageproc.Source{}, imageproc.Transform{
		Width: 1, Height: 1, Quality: 50, Format: imageopt.FormatWebP,
	}); !errors.Is(err, imageproc.ErrBackendUnavailable) {
		t.Fatalf("noop error: %v", err)
	}
}
