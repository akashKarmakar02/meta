package httpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/akashKarmakar02/meta/imageproc"
)

func newTestResolver(t *testing.T, mapping map[string]string) *ImageResolver {
	t.Helper()
	r, err := NewImageResolver(mapping, DefaultImageSourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// TestResolverValidFile: a real file under an approved root resolves.
func TestResolverValidFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("jpeg-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newTestResolver(t, map[string]string{"/images/": dir})
	b, err := r.Resolve(context.Background(), "/images/hero.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "jpeg-bytes" {
		t.Fatalf("content mismatch: %q", b)
	}
}

// TestResolverMissingFile fails as not-found.
func TestResolverMissingFile(t *testing.T) {
	dir := t.TempDir()
	r := newTestResolver(t, map[string]string{"/images/": dir})
	if _, err := r.Resolve(context.Background(), "/images/nope.jpg"); !errors.Is(err, errImageSourceNotFound) {
		t.Fatalf("want not-found, got %v", err)
	}
}

// TestResolverDirectoryAsFile: a directory identity is not a source.
func TestResolverDirectoryAsFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := newTestResolver(t, map[string]string{"/images/": dir})
	if _, err := r.Resolve(context.Background(), "/images/sub"); !errors.Is(err, errImageSourceNotFound) {
		t.Fatalf("directory want not-found, got %v", err)
	}
}

// TestResolverSymlinkInsideRoot: a relative symlink to another file
// inside the same root resolves — legitimate public content aliasing.
// (os.Root resolves relative symlinks within its scope; absolute-path
// symlinks are refused since they escape its lexical root.)
func TestResolverSymlinkInsideRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.jpg"), []byte("aaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.jpg", filepath.Join(dir, "alias.jpg")); err != nil {
		t.Fatal(err)
	}
	r := newTestResolver(t, map[string]string{"/images/": dir})
	b, err := r.Resolve(context.Background(), "/images/alias.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "aaa" {
		t.Fatalf("alias content: %q", b)
	}
}

// TestResolverSymlinkEscape: a symlink pointing outside the root is
// refused by the root-scoped open. It must fail closed.
func TestResolverSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "leak.jpg")); err != nil {
		t.Fatal(err)
	}
	r := newTestResolver(t, map[string]string{"/images/": dir})
	_, err := r.Resolve(context.Background(), "/images/leak.jpg")
	if err == nil {
		t.Fatal("symlink escape resolved")
	}
	if errors.Is(err, errImageSourceNotFound) && err.Error() == "image source not found" {
		// fine: 404 mapping
	}
}

// TestResolverParentTraversal: dot-dot segments are refused by OpenRoot.
func TestResolverParentTraversal(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := newTestResolver(t, map[string]string{"/images/": dir})
	for _, ident := range []string{
		"/images/../secret",
		"/images/../../etc/passwd",
		"/images/%2e%2e/private",
		"/images/./x",
	} {
		if _, err := r.Resolve(context.Background(), ident); err == nil {
			t.Fatalf("%s: traversal resolved", ident)
		}
	}
}

// TestResolverAbsolutePath: an absolute filesystem path inside a
// namespace tail cannot escape; also an unsupported namespace fails.
func TestResolverAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	r := newTestResolver(t, map[string]string{"/images/": dir})
	if _, err := r.Resolve(context.Background(), "/images//etc/passwd"); err == nil {
		t.Fatal("absolute path resolved")
	}
	if _, err := r.Resolve(context.Background(), "/private/etc/passwd"); !errors.Is(err, errImageSourceNotFound) {
		t.Fatalf("unsupported namespace: %v", err)
	}
}

// TestResolverBackslashAndNUL: backslash and NUL never reach the
// filesystem as separators or terminators (OpenRoot rejects them).
func TestResolverBackslashAndNUL(t *testing.T) {
	dir := t.TempDir()
	r := newTestResolver(t, map[string]string{"/images/": dir})
	for _, ident := range []string{
		"/images/..\\..\\etc\\passwd",
		"/images/a\x00b.jpg",
	} {
		if _, err := r.Resolve(context.Background(), ident); err == nil {
			t.Fatalf("%q: unsafe identity resolved", ident)
		}
	}
}

// TestResolverSizeLimit: an oversized source is refused before allocation.
func TestResolverSizeLimit(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, 64)
	if err := os.WriteFile(filepath.Join(dir, "big.jpg"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := NewImageResolver(map[string]string{"/images/": dir}, ImageSourceLimits{
		MaxSourceBytes:   16,
		MaxDecodedPixels: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Resolve(context.Background(), "/images/big.jpg"); !errors.Is(err, imageproc.ErrSourceTooLarge) {
		t.Fatalf("want too-large, got %v", err)
	}
}

// TestResolverCanceledContext: cancellation before resolution returns.
func TestResolverCanceledContext(t *testing.T) {
	dir := t.TempDir()
	r := newTestResolver(t, map[string]string{"/images/": dir})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Resolve(ctx, "/images/x.jpg"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ctx: %v", err)
	}
}

// TestResolverNewlineIdentity cannot resolve (OpenRoot rejects control
// characters in names only if present on disk; absence makes it 404,
// which is safe either way).
func TestResolverNewlineIdentity(t *testing.T) {
	dir := t.TempDir()
	r := newTestResolver(t, map[string]string{"/images/": dir})
	if _, err := r.Resolve(context.Background(), "/images/a\n..jpg"); err == nil {
		t.Fatal("newline identity resolved")
	}
}

// TestResolverConfigValidation: empty mapping and invalid limits fail at
// construction.
func TestResolverConfigValidation(t *testing.T) {
	if _, err := NewImageResolver(nil, DefaultImageSourceLimits()); err == nil {
		t.Fatal("empty mapping accepted")
	}
	if _, err := NewImageResolver(map[string]string{"images": "/tmp"}, DefaultImageSourceLimits()); err == nil {
		t.Fatal("namespace without slashes accepted")
	}
	if _, err := NewImageResolver(map[string]string{"/images/": t.TempDir()}, ImageSourceLimits{}); err == nil {
		t.Fatal("zero limits accepted")
	}
}

// TestCheckDecodedPixels: overflow-safe pixel ceiling.
func TestCheckDecodedPixels(t *testing.T) {
	l := ImageSourceLimits{MaxSourceBytes: 1, MaxDecodedPixels: 1000}
	if err := l.CheckDecodedPixels(100, 10); err != nil {
		t.Fatal(err)
	}
	if err := l.CheckDecodedPixels(1<<20, 1<<20); err != imageproc.ErrSourceTooLarge && err == nil {
		t.Fatalf("huge dims passed: %v", err)
	}
	if err := l.CheckDecodedPixels(0, 10); err != imageproc.ErrSourceInvalid && err == nil {
		t.Fatalf("zero dim passed: %v", err)
	}
}
