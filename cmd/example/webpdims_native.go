//go:build imageproc_native

package main

import (
	"image"
	"testing"

	"go-meta/internal/imageprocessor"
)

// imageDecodeDims decodes a WebP response body through the native decoder.
func imageDecodeDims(t *testing.T, data []byte) (int, int) {
	t.Helper()
	img, err := imageprocessor.DecodeWebPForTest(data)
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	return b.Dx(), b.Dy()
}

var _ = image.Config{}
