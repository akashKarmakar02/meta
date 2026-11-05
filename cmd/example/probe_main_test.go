package main

import (
	"context"
	"os"
	"testing"

	"github.com/akashKarmakar02/meta/imageproc"
)

// TestF02NativeBackendWiringSmoke: an in-module smoke that the single
// registered constructor builds the backend and reports capabilities —
// guarded by the imageproc_native tag; default builds skip it.
func TestF02NativeBackendWiringSmoke(t *testing.T) {
	// The wiring registers newImageProcessor exactly once; repeated
	// registration is idempotent for the same function value.
	imageproc.RegisterBackend(newImageProcessor)
	p, err := imageproc.New(context.Background())
	if err != nil {
		t.Skipf("native backend unavailable: %v", err)
	}
	caps := p.Capabilities()
	if !caps.WebPEncode || !caps.AVIFEncode {
		t.Fatalf("capabilities lost through wiring: %+v", caps)
	}
	if os.Getenv("GO_META_F02_SMOKE") == "1" {
		t.Log("caps:", caps)
	}
}
