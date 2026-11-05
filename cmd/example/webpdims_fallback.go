//go:build !imageproc_native

package main

import "testing"

// imageDecodeDims is unreachable in untagged builds (the test skips), but
// keeps the file compiling for both build shapes.
func imageDecodeDims(t *testing.T, data []byte) (int, int) {
	t.Helper()
	t.Skip("no native decoder in untagged builds")
	return 0, 0
}
