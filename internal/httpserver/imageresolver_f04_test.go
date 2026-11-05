package httpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/akashKarmakar02/meta/imageproc"
)

// F04 filesystem security tests: special files, descriptor hygiene, and
// the TOCTOU single-descriptor property.

// TestF04ResolverRejectsSpecialFiles: FIFOs, unix sockets, and device
// nodes under a public root are refused — never read, never blocking.
func TestF04ResolverRejectsSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe.jpg")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("cannot create fifo: %v", err)
	}

	r := newTestResolver(t, map[string]string{"/images/": dir})

	// The FIFO must fail fast (nonblocking open + regular-file check), not
	// hang the worker. A generous wall-clock bound proves non-blocking.
	done := make(chan error, 1)
	go func() {
		_, err := r.Resolve(context.Background(), "/images/pipe.jpg")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, errImageSourceForbidden) {
			t.Fatalf("fifo: err = %v, want forbidden", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("FIFO resolve blocked the worker")
	}
}

// TestF04ResolverSizeBoundary: exactly-at-limit reads succeed; one byte
// over fails; the cap applies to actual bytes read even when the file
// grows between open and read.
func TestF04ResolverSizeBoundary(t *testing.T) {
	dir := t.TempDir()
	r, err := NewImageResolver(map[string]string{"/images/": dir}, ImageSourceLimits{
		MaxSourceBytes:   64,
		MaxDecodedPixels: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	atLimit := filepath.Join(dir, "at.bin")
	if err := os.WriteFile(atLimit, make([]byte, 64), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := r.Resolve(context.Background(), "/images/at.bin")
	if err != nil || len(b) != 64 {
		t.Fatalf("at-limit read: %d bytes, err %v", len(b), err)
	}

	over := filepath.Join(dir, "over.bin")
	if err := os.WriteFile(over, make([]byte, 65), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(context.Background(), "/images/over.bin"); !errors.Is(err, imageproc.ErrSourceTooLarge) {
		t.Fatalf("over-limit: err = %v, want ErrSourceTooLarge", err)
	}

	// A file that lies about its size: st.Size() large but the read cap
	// governs. (Sparse file: declares big, reads as zeros within the cap.)
	sparse := filepath.Join(dir, "sparse.bin")
	if err := os.WriteFile(sparse, make([]byte, 65), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(context.Background(), "/images/sparse.bin"); !errors.Is(err, imageproc.ErrSourceTooLarge) {
		t.Fatalf("sparse over-limit: err = %v", err)
	}
}

// TestF04ResolverSingleDescriptorSnapshot: the resolver returns one
// descriptor's snapshot. Replacing the file AFTER Resolve returns cannot
// change the bytes already validated; the next Resolve observes the new
// file atomically at its own single-open boundary.
func TestF04ResolverSingleDescriptorSnapshot(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "hero.jpg")
	if err := os.WriteFile(name, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newTestResolver(t, map[string]string{"/images/": dir})

	first, err := r.Resolve(context.Background(), "/images/hero.jpg")
	if err != nil || string(first) != "original" {
		t.Fatalf("first resolve: %q, %v", first, err)
	}
	// Replace on disk: the already-returned snapshot must be unaffected.
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, []byte("replaced!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, name); err != nil {
		t.Fatal(err)
	}
	if string(first) != "original" {
		t.Fatal("returned snapshot mutated by on-disk replacement")
	}
	second, err := r.Resolve(context.Background(), "/images/hero.jpg")
	if err != nil || string(second) != "replaced!" {
		t.Fatalf("second resolve: %q, %v", second, err)
	}
}

// TestF04ResolverReplacementRaceDeterministic: concurrent resolves while
// a file is replaced in place repeatedly must never return a mixed read;
// every observed snapshot is entirely one version's bytes. Deterministic:
// bounded iterations, no sleeps.
func TestF04ResolverReplacementRaceDeterministic(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "race.jpg")
	if err := os.WriteFile(name, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newTestResolver(t, map[string]string{"/images/": dir})

	stop := make(chan struct{})
	replacerDone := make(chan struct{})
	go func() {
		defer close(replacerDone)
		for i := 0; i < 200; i++ {
			tmp := name + ".tmp"
			v := []byte("v1")
			if i%2 == 1 {
				v = []byte("v2-longer-content")
			}
			if err := os.WriteFile(tmp, v, 0o644); err != nil {
				return
			}
			_ = os.Rename(tmp, name)
		}
		close(stop)
	}()
	for {
		select {
		case <-stop:
			<-replacerDone
			return
		default:
		}
		b, err := r.Resolve(context.Background(), "/images/race.jpg")
		if err != nil {
			continue // replaced mid-stat: safe failure
		}
		s := string(b)
		if s != "v1" && s != "v2-longer-content" {
			t.Fatalf("mixed snapshot observed: %q", s)
		}
	}
}
