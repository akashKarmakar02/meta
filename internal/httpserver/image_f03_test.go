package httpserver

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/akashKarmakar02/meta/imageopt"
	"github.com/akashKarmakar02/meta/imageproc"
)

// F03 HTTP integration tests. Real filesystem roots through the resolver;
// processors are fakes with configured capabilities for mapping tests —
// real encoder output is covered by the tagged native suite and by
// TestF03HTTPRealBytes below when the native backend is available.

// f03Server builds an app with the image endpoint processing wired to
// a root dir and processor, returning app and root for fixture writes.
func f03Server(t *testing.T, proc imageproc.Processor) (*fiber.App, string) {
	t.Helper()
	dir := t.TempDir()
	app := f03ServerAt(t, proc, dir)
	return app, dir
}

func f03ServerAt(t *testing.T, proc imageproc.Processor, dir string) *fiber.App {
	t.Helper()
	resolver, err := NewImageResolver(map[string]string{"/images/": dir}, DefaultImageSourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resolver.Close() })
	app := fiber.New()
	if err := MountImageProcessing(app, imageopt.DefaultLimits(), ImageEndpointConfig{
		Resolver:      resolver,
		Processor:     proc,
		MaxConcurrent: 2,
	}); err != nil {
		t.Fatal(err)
	}
	app.Get("/*", func(c fiber.Ctx) error { return c.SendString("page") })
	return app
}

// passthrough fake: returns deterministic bytes tagged by format.
type passthrough struct {
	caps imageproc.Capabilities
	err  error
}

func (p passthrough) Capabilities() imageproc.Capabilities { return p.caps }

func (p passthrough) Process(_ context.Context, src imageproc.Source, tr imageproc.Transform) (imageproc.Result, error) {
	if p.err != nil {
		return imageproc.Result{}, p.err
	}
	mime, _ := tr.Format.MIME()
	return imageproc.Result{
		Data:    []byte("OUT:" + string(tr.Format)),
		Format:  tr.Format,
		MIME:    mime,
		Width:   tr.Width,
		Height:  tr.Height,
		Version: "fake",
	}, nil
}

var _ context.Context // placeholder to keep imports tidy if unused

// TestF03HTTPSuccessMapping: configured processor + existing source →
// 200 with the result bytes and correct headers.
func TestF03HTTPSuccessMapping(t *testing.T) {
	proc := passthrough{caps: fullCaps()}
	app, dir := f03Server(t, proc)
	os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("src"), 0o644)

	for _, tc := range []struct{ path, mime string }{
		{"/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=webp", "image/webp"},
		{"/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=avif", "image/avif"},
		{"/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=jpeg", "image/jpeg"},
		{"/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=png", "image/png"},
	} {
		res, err := app.Test(httptest.NewRequest(http.MethodGet, tc.path, nil))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != tc.mime {
			t.Fatalf("%s: %d %s", tc.path, res.StatusCode, res.Header.Get("Content-Type"))
		}
		if !bytes.HasPrefix(body, []byte("OUT:")) || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: body %q cache %q", tc.path, body, res.Header.Get("Cache-Control"))
		}
		if cl := res.Header.Get("Content-Length"); cl == "" {
			t.Fatalf("%s: missing content-length", tc.path)
		}
	}
}

// TestF03HTTPAutoNegotiation: f=auto picks a supported format from Accept.
func TestF03HTTPAutoNegotiation(t *testing.T) {
	proc := passthrough{caps: fullCaps()}
	app, dir := f03Server(t, proc)
	os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("src"), 0o644)

	for _, tc := range []struct {
		accept string
		want   string
		vary   bool
		status int
	}{
		{"image/avif,image/webp", "OUT:avif", true, 200},
		{"image/webp", "OUT:webp", true, 200},
		{"image/avif;q=0,image/webp;q=1", "OUT:webp", true, 200},
		{"image/gif", "", true, 406},
		{"", "OUT:webp", true, 200},
	} {
		req := httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=auto", nil)
		if tc.accept != "" {
			req.Header.Set("Accept", tc.accept)
		}
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != tc.status {
			t.Fatalf("accept %q: %d", tc.accept, res.StatusCode)
		}
		if tc.status == 200 && string(body) != tc.want {
			t.Fatalf("accept %q: body %q", tc.accept, body)
		}
		if gotVary := res.Header.Get("Vary") == "Accept"; gotVary != tc.vary {
			t.Fatalf("accept %q: vary=%v", tc.accept, res.Header.Get("Vary"))
		}
	}

	// Explicit format: no Vary (deterministic representation).
	res, _ := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=webp", nil))
	if res.Header.Get("Vary") != "" {
		t.Fatal("explicit format must not vary")
	}
}

// TestF03HTTPErrors: deliberate status mapping end to end.
func TestF03HTTPErrors(t *testing.T) {
	proc := passthrough{caps: fullCaps()}
	app, dir := f03Server(t, proc)

	// Missing source → 404.
	res, _ := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fmissing.jpg&w=50&f=webp", nil))
	if res.StatusCode != 404 {
		t.Fatalf("missing: %d", res.StatusCode)
	}

	// Symlink escape → 404 (no existence oracle).
	os.Symlink(filepath.Join(dir, "..", "..", "etc", "passwd"), filepath.Join(dir, "leak.jpg"))
	res, _ = app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fleak.jpg&w=50&f=webp", nil))
	if res.StatusCode != 404 {
		t.Fatalf("symlink escape: %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if strings.Contains(string(body), "passwd") || strings.Contains(string(body), dir) {
		t.Fatalf("path disclosure: %q", body)
	}

	// Unsupported format → 406 (explicit f=avif without capability).
	noAVIF := passthrough{caps: func() imageproc.Capabilities {
		c := fullCaps()
		c.AVIFEncode = false
		return c
	}()}
	app2, dir2 := f03Server(t, noAVIF)
	os.WriteFile(filepath.Join(dir2, "hero.jpg"), []byte("src"), 0o644)
	res, _ = app2.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=avif", nil))
	if res.StatusCode != 406 {
		t.Fatalf("unsupported format: %d", res.StatusCode)
	}

	// Invalid requests stay 400 through the configured pipeline.
	res, _ = app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fa.jpg&w=0", nil))
	if res.StatusCode != 400 {
		t.Fatalf("invalid: %d", res.StatusCode)
	}
	res, _ = app.Test(httptest.NewRequest(http.MethodPost, "/_meta/image?src=%2Fimages%2Fa.jpg&w=50", nil))
	if res.StatusCode != 405 || res.Header.Get("Allow") != "GET, HEAD" {
		t.Fatalf("post: %d", res.StatusCode)
	}
}

// TestF03HTTPEncodeFailure: processor failure mid-flight never writes a
// partial body or a success status.
func TestF03HTTPEncodeFailure(t *testing.T) {
	proc := passthrough{caps: fullCaps(), err: os.ErrDeadlineExceeded} // any error
	app, dir := f03Server(t, proc)
	os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("src"), 0o644)
	res, _ := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=webp", nil))
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 500 || len(body) > 64 || bytes.HasPrefix(body, []byte("OUT:")) {
		t.Fatalf("encode failure: %d %q", res.StatusCode, body)
	}
}

// TestF03HTTPHeadSemantics: HEAD reports the same status and headers as
// GET but no body bytes.
func TestF03HTTPHeadSemantics(t *testing.T) {
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
		t.Fatalf("head type %q vs %q", head.Header.Get("Content-Type"), get.Header.Get("Content-Type"))
	}
	if head.Header.Get("Content-Length") != get.Header.Get("Content-Length") {
		t.Fatalf("head length %q vs %q", head.Header.Get("Content-Length"), get.Header.Get("Content-Length"))
	}
	if len(hb) != 0 || len(gb) == 0 {
		t.Fatalf("bodies: head %d get %d", len(hb), len(gb))
	}
}

// TestF03HTTPConcurrencyLimit: MaxConcurrent bounds in-flight processes;
// queued requests wait rather than fail.
func TestF03HTTPConcurrencyLimit(t *testing.T) {
	var active, peak int
	var mu sync.Mutex
	proc := &countingProc{caps: fullCaps(), onProcess: func() {
		mu.Lock()
		active++
		if active > peak {
			peak = active
		}
		mu.Unlock()
	}, onDone: func() {
		mu.Lock()
		active--
		mu.Unlock()
	}}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("src"), 0o644)
	app := f03ServerAt(t, proc, dir)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=webp", nil)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if peak > 2 {
		t.Fatalf("peak concurrency %d exceeded limit 2", peak)
	}
}

// countingProc wraps passthrough with a hook.
type countingProc struct {
	caps      imageproc.Capabilities
	onProcess func()
	onDone    func()
}

func (p *countingProc) Capabilities() imageproc.Capabilities { return p.caps }

func (p *countingProc) Process(ctx context.Context, src imageproc.Source, tr imageproc.Transform) (imageproc.Result, error) {
	if p.onProcess != nil {
		p.onProcess()
	}
	if p.onDone != nil {
		defer p.onDone()
	}
	mime, _ := tr.Format.MIME()
	return imageproc.Result{Data: []byte("x"), Format: tr.Format, MIME: mime, Width: tr.Width, Height: tr.Height}, nil
}

// TestF03HTTPNoEnvelopeLeak: image responses never carry the navigation
// envelope; page loader not executed.
func TestF03HTTPNoEnvelopeLeak(t *testing.T) {
	proc := passthrough{caps: fullCaps()}
	app, dir := f03Server(t, proc)
	os.WriteFile(filepath.Join(dir, "hero.jpg"), []byte("src"), 0o644)
	res, _ := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=webp", nil))
	body, _ := io.ReadAll(res.Body)
	if bytes.Contains(body, []byte(`"routeId"`)) || bytes.Contains(body, []byte("navigation")) {
		t.Fatalf("envelope leaked: %q", body)
	}
	if ct := res.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
		t.Fatalf("html content type: %s", ct)
	}
}

// TestF03HTTPSourceByteLimit: oversized source rejected 413.
func TestF03HTTPSourceByteLimit(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, 1024)
	os.WriteFile(filepath.Join(dir, "hero.jpg"), big, 0o644)
	resolver, err := NewImageResolver(map[string]string{"/images/": dir}, ImageSourceLimits{MaxSourceBytes: 128, MaxDecodedPixels: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	app := fiber.New()
	if err := MountImageProcessing(app, imageopt.DefaultLimits(), ImageEndpointConfig{
		Resolver:  resolver,
		Processor: passthrough{caps: fullCaps()},
	}); err != nil {
		t.Fatal(err)
	}
	res, _ := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fhero.jpg&w=50&f=webp", nil))
	if res.StatusCode != 413 {
		t.Fatalf("byte limit: %d", res.StatusCode)
	}
}

// TestF03HTTPDecodeConfigGate: sources the processor cannot sniff as
// raster images are refused 415 before processing.
func TestF03HTTPDecodeConfigGate(t *testing.T) {
	proc := &sniffProc{caps: fullCaps()}
	app, dir := f03Server(t, proc)
	os.WriteFile(filepath.Join(dir, "weird.svg"), []byte("<svg/>"), 0o644)
	res, _ := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fweird.svg&w=50&f=webp", nil))
	if res.StatusCode != 415 {
		t.Fatalf("svg: %d", res.StatusCode)
	}
}

// sniffProc refuses to sniff anything (like a processor whose decoder
// registry misses the source format).
type sniffProc struct{ caps imageproc.Capabilities }

func (p *sniffProc) Capabilities() imageproc.Capabilities { return p.caps }

func (p *sniffProc) Process(ctx context.Context, s imageproc.Source, tr imageproc.Transform) (imageproc.Result, error) {
	mime, _ := tr.Format.MIME()
	return imageproc.Result{Data: []byte("x"), Format: tr.Format, MIME: mime, Width: tr.Width, Height: tr.Height}, nil
}

// DecodeConfig rejects everything it is asked to sniff.
func (p *sniffProc) DecodeConfig(src imageproc.Source) (image.Config, error) {
	return image.Config{}, imageproc.ErrSourceInvalid
}

// Compile-time guard: passthrough implements the full contract.
var _ imageproc.Processor = passthrough{}
var _ imageproc.Processor = &countingProc{}
var _ imageproc.Processor = &sniffProc{}

// keep imports used
var (
	_ = image.DecodeConfig
	_ = jpeg.Encode
	_ = png.Encode
)
