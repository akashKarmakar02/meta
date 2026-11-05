package httpserver

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"go-meta/imageproc"
)

func TestF06ResolverChoosesLongestNamespace(t *testing.T) {
	broad, specific := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(broad, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broad, "nested", "a"), []byte("broad"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specific, "a"), []byte("specific"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := NewImageResolver(map[string]string{"/images/": broad, "/images/nested/": specific}, DefaultImageSourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for range 100 {
		v, err := r.Resolve(context.Background(), "/images/nested/a")
		if err != nil || string(v) != "specific" {
			t.Fatalf("namespace ambiguity: %q %v", v, err)
		}
	}
	if ns := r.Namespaces(); len(ns) != 2 || ns[0] != "/images/nested/" {
		t.Fatalf("namespace order %v", ns)
	}
}
func TestF06DecodedPixelsOverflow(t *testing.T) {
	l := DefaultImageSourceLimits()
	if err := l.CheckDecodedPixels(math.MaxInt, math.MaxInt); !errors.Is(err, imageproc.ErrSourceTooLarge) {
		t.Fatalf("overflow: %v", err)
	}
	if err := (ImageSourceLimits{MaxSourceBytes: math.MaxInt64, MaxDecodedPixels: 1}).Validate(); err == nil {
		t.Fatal("source read limit overflow accepted")
	}
}
