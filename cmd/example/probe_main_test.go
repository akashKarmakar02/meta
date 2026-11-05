package main

import (
	"context"
	"os"
	"testing"

	"go-meta/imageproc"
	"go-meta/internal/imageprocessor"
)

// TestF02NativeBackendWiringSmoke: an in-module smoke that the native
// backend constructs, registers, and reports capabilities — guarded by the
// imageproc_native tag, so default builds skip it.
func TestF02NativeBackendWiringSmoke(t *testing.T) {
	backend, err := imageprocessor.New(context.Background())
	if err != nil {
		t.Skipf("native backend unavailable: %v", err)
	}
	imageproc.RegisterBackend(func(ctx context.Context) (imageproc.Processor, error) {
		return backend, nil
	})
	_ = backend
	p, err := imageproc.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	caps := p.Capabilities()
	if !caps.WebPEncode || !caps.AVIFEncode {
		t.Fatalf("capabilities lost through wiring: %+v", caps)
	}
	if os.Getenv("GO_META_F02_SMOKE") == "1" {
		t.Log("caps:", caps)
	}
}
