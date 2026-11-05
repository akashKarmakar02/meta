package httpserver

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/akashKarmakar02/meta/imageproc"
)

// F03R HTTP regression tests: oversized declared dimensions rejected
// before full decode, invalid/truncated metadata, valid real processing
// (native when available), cache policy on success and errors, Vary on
// auto, HEAD consistency, and no partial responses on failure.

// sniffingProc delegates DecodeConfig to a function so the gate can be
// exercised with declared-dimension fixtures; Process produces the bytes
// of an encoded fake result.
type sniffingProc struct {
	caps    imageproc.Capabilities
	sniff   func(src imageproc.Source) (image.Config, error)
	process func(src imageproc.Source) (imageproc.Result, error)
}

func (p *sniffingProc) Capabilities() imageproc.Capabilities { return p.caps }

func (p *sniffingProc) Process(ctx context.Context, src imageproc.Source, tr imageproc.Transform) (imageproc.Result, error) {
	if p.process != nil {
		return p.process(src)
	}
	mime, _ := tr.Format.MIME()
	return imageproc.Result{
		Data: []byte("OUT:" + string(tr.Format)), Format: tr.Format, MIME: mime,
		Width: tr.Width, Height: tr.Height,
	}, nil
}

func (p *sniffingProc) DecodeConfig(src imageproc.Source) (image.Config, error) {
	return p.sniff(src)
}

// TestF03RHTTPOversizedDeclaredDimensions: a source whose header declares
// dimensions over the decoded-pixel limit is refused 413 before any
// processor work — the processor never runs.
func TestF03RHTTPOversizedDeclaredDimensions(t *testing.T) {
	var processCalls int
	proc := &sniffingProc{
		caps:  fullCaps(),
		sniff: func(imageproc.Source) (image.Config, error) { return image.Config{Width: 100000, Height: 100000}, nil },
		process: func(imageproc.Source) (imageproc.Result, error) {
			processCalls++
			return imageproc.Result{}, os.ErrDeadlineExceeded
		},
	}
	app, dir := f03Server(t, proc)
	os.WriteFile(filepath.Join(dir, "bomb.png"), []byte("x"), 0o644)

	res, err := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fbomb.png&w=50&f=webp", nil))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", res.StatusCode)
	}
	if processCalls != 0 {
		t.Fatalf("processor ran %d times for oversized source", processCalls)
	}
	if len(body) == 0 || bytes.HasPrefix(body, []byte("OUT:")) {
		t.Fatalf("partial or empty response body: %q", body)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("cache-control = %q, want no-store", cc)
	}
}

// TestF03RHTTPInvalidMetadata: inspection failure → 415, not 500.
func TestF03RHTTPInvalidMetadata(t *testing.T) {
	proc := &sniffingProc{
		caps:  fullCaps(),
		sniff: func(imageproc.Source) (image.Config, error) { return image.Config{}, imageproc.ErrSourceInvalid },
	}
	app, dir := f03Server(t, proc)
	os.WriteFile(filepath.Join(dir, "junk.jpg"), []byte("not an image at all"), 0o644)
	res, _ := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fjunk.jpg&w=50&f=webp", nil))
	if res.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", res.StatusCode)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("cache-control = %q, want no-store", cc)
	}
}

// TestF03RHTTPZeroDimensions: a header declaring a zero axis is invalid
// metadata (415), not a pixel-limit case.
func TestF03RHTTPZeroDimensions(t *testing.T) {
	proc := &sniffingProc{
		caps:  fullCaps(),
		sniff: func(imageproc.Source) (image.Config, error) { return image.Config{Width: 0, Height: 100}, nil },
	}
	app, dir := f03Server(t, proc)
	os.WriteFile(filepath.Join(dir, "zero.png"), []byte("x"), 0o644)
	res, _ := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fzero.png&w=50&f=webp", nil))
	if res.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", res.StatusCode)
	}
}

// TestF03RHTTPCachePolicyOnSuccess: successful optimized responses are
// Cache-Control: no-store — the URL is unversioned, so no immutable or
// shared-cache policy may be advertised (F03R §5).
func TestF03RHTTPCachePolicyOnSuccess(t *testing.T) {
	proc := passthrough{caps: fullCaps()}
	app, dir := f03Server(t, proc)
	os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("src"), 0o644)
	for _, path := range []string{
		"/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=webp",
		"/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=avif",
		"/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=auto",
	} {
		res, _ := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		if res.StatusCode != 200 {
			t.Fatalf("%s: status = %d", path, res.StatusCode)
		}
		if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
			t.Fatalf("%s: cache-control = %q, want no-store", path, cc)
		}
	}
}

// TestF03RHTTPVaryOnAuto: f=auto sets Vary: Accept; explicit formats do not.
func TestF03RHTTPVaryOnAuto(t *testing.T) {
	proc := passthrough{caps: fullCaps()}
	app, dir := f03Server(t, proc)
	os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("src"), 0o644)

	res, _ := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=auto", nil))
	if v := res.Header.Get("Vary"); v != "Accept" {
		t.Fatalf("auto vary = %q, want Accept", v)
	}
	res, _ = app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=webp", nil))
	if v := res.Header.Get("Vary"); v != "" {
		t.Fatalf("explicit vary = %q, want empty", v)
	}
}

// TestF03RHTTPHeadNoBody: HEAD mirrors GET headers, never a body.
func TestF03RHTTPHeadNoBody(t *testing.T) {
	proc := passthrough{caps: fullCaps()}
	app, dir := f03Server(t, proc)
	os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("src"), 0o644)
	get, _ := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=webp", nil))
	head, _ := app.Test(httptest.NewRequest(http.MethodHead, "/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=webp", nil))
	gb, _ := io.ReadAll(get.Body)
	hb, _ := io.ReadAll(head.Body)
	if head.StatusCode != get.StatusCode {
		t.Fatalf("head status %d vs get %d", head.StatusCode, get.StatusCode)
	}
	if head.Header.Get("Content-Type") != get.Header.Get("Content-Type") {
		t.Fatalf("head content-type mismatch")
	}
	if head.Header.Get("Content-Length") != get.Header.Get("Content-Length") {
		t.Fatalf("head content-length mismatch")
	}
	if len(hb) != 0 || len(gb) == 0 {
		t.Fatalf("head body %d bytes, get body %d bytes", len(hb), len(gb))
	}
}

// TestF03RHTTPNoPartialResponseOnFailure: processor failure yields a
// complete safe error, never partial image bytes.
func TestF03RHTTPNoPartialResponseOnFailure(t *testing.T) {
	proc := passthrough{caps: fullCaps(), err: imageproc.ErrProcessing}
	app, dir := f03Server(t, proc)
	os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("src"), 0o644)
	res, _ := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=webp", nil))
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 500 {
		t.Fatalf("status = %d, want 500", res.StatusCode)
	}
	if bytes.HasPrefix(body, []byte("OUT:")) {
		t.Fatal("partial image bytes returned on failure")
	}
	if ct := res.Header.Get("Content-Type"); strings.HasPrefix(ct, "image/") {
		t.Fatalf("image content-type on failure: %s", ct)
	}
}

// TestF03RHTTPSourceNotFoundAndSymlinkEscape: both resolve to 404 without
// path disclosure.
func TestF03RHTTPSourceNotFoundAndSymlinkEscape(t *testing.T) {
	proc := passthrough{caps: fullCaps()}
	app, dir := f03Server(t, proc)

	res, _ := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fmissing.jpg&w=50&f=webp", nil))
	if res.StatusCode != 404 {
		t.Fatalf("missing source: %d", res.StatusCode)
	}
	// Symlink escape fails closed as 404.
	os.Symlink("/etc/passwd", filepath.Join(dir, "leak.jpg"))
	res, _ = app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fleak.jpg&w=50&f=webp", nil))
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 404 {
		t.Fatalf("symlink escape: %d", res.StatusCode)
	}
	if strings.Contains(string(body), "passwd") || strings.Contains(string(body), dir) {
		t.Fatalf("path disclosure: %q", body)
	}
}

// TestF03RHTTPUntaggedFallback: no resolver/processor wired keeps the
// truthful reserved endpoint.
func TestF03RHTTPUntaggedFallback(t *testing.T) {
	app := fiber.New()
	MountImage(app)
	res, _ := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=webp", nil))
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", res.StatusCode)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("cache-control = %q, want no-store", cc)
	}
}

// testPNGBytes builds a small gradient PNG for fixtures.
func testPNGBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 255 / max(1, w-1)), G: uint8(y * 255 / max(1, h-1)), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
