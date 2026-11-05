package imageproc_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// TestF02ExternalModuleImport (§29): an isolated external Go module
// imports go-meta/imageopt and go-meta/imageproc without Fiber, templ,
// or native image libraries in its dependency graph. imageproc defines
// the processor contract; applications wire a concrete backend through
// configuration, not by importing internal packages.
func TestF02ExternalModuleImport(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	module := "module example.test/consumer\n\ngo 1.26.3\n\nrequire go-meta v0.0.0\nreplace go-meta => " + strconv.Quote(root) + "\n"
	source := `package consumer

import (
	"context"
	"testing"

	"go-meta/imageopt"
	"go-meta/imageproc"
)

func TestExternalF02(t *testing.T) {
	// Build a validated request and transform from public types only.
	req := imageopt.Request{Source: "/images/hero.jpg", Width: 960, Quality: 80, Format: imageopt.FormatWebP}
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	url, err := imageopt.URL(req)
	if err != nil || url == "" {
		t.Fatalf("URL: %v %q", err, url)
	}

	// The processor contract is usable externally: an application can
	// define its own Processor implementation.
	var p imageproc.Processor = appProcessor{}
	if !p.Capabilities().WebPEncode {
		t.Fatal("capabilities not visible")
	}
	transform := imageproc.Transform{Width: 64, Height: 64, Quality: 80, Format: imageopt.FormatWebP}
	if err := imageproc.ValidateTransform(transform); err != nil {
		t.Fatal(err)
	}
	src := imageproc.Source{URLIdentity: "/images/hero.jpg", Bytes: []byte("x")}
	res, err := p.Process(context.Background(), src, transform)
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != imageopt.FormatWebP || res.MIME != "image/webp" || len(res.Data) == 0 {
		t.Fatalf("result: %+v", res)
	}

	// A backend-less program still constructs requests; New fails closed
	// rather than pretending processing exists.
	if _, err := imageproc.New(context.Background()); err == nil {
		t.Fatal("expected ErrBackendUnavailable without a registered backend")
	}
}

type appProcessor struct{}

func (appProcessor) Capabilities() imageproc.Capabilities {
	return imageproc.Capabilities{WebPEncode: true}
}

func (appProcessor) Process(ctx context.Context, src imageproc.Source, tr imageproc.Transform) (imageproc.Result, error) {
	return imageproc.Result{
		Data: []byte("webp"), Format: tr.Format, MIME: "image/webp",
		Width: tr.Width, Height: tr.Height, Version: "app-v1",
	}, nil
}
`
	files := map[string]string{
		"go.mod":           module,
		"consumer_test.go": source,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("external module test failed:\n%s", out)
	}
	// The dependency graph must stay free of Fiber/templ/native image code.
	list := exec.Command("go", "list", "-deps", "./...")
	list.Dir = dir
	list.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	deps, err := list.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %s", deps)
	}
	for banned := range map[string]bool{
		"github.com/gofiber/fiber/v3": true,
		"github.com/a-h/templ":        true,
		"github.com/google/pprof":     true,
	} {
		if containsLine(string(deps), banned) {
			t.Fatalf("dependency graph pulled in %s", banned)
		}
	}
}

func containsLine(haystack, needle string) bool {
	for _, line := range splitLines(haystack) {
		if line == needle {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
