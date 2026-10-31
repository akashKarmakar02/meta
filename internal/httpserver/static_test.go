package httpserver

import (
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// fixtureClientDir stages a minimal dist/client tree: hashed entry,
// a dynamic page chunk, CSS, and the manifest itself.
func fixtureClientDir(t *testing.T) string {
	t.Helper()
	manifest := `{
  "frontend/entry.ts": {
    "file": "assets/entry-TEST01.js",
    "isEntry": true,
    "css": ["assets/entry-TEST01.css"]
  },
  "app/dashboard/page.svelte": {
    "file": "assets/page-TEST02.js",
    "isDynamicEntry": true
  }
}`
	files := map[string]string{
		".vite/manifest.json":     manifest,
		"assets/entry-TEST01.js":  "// console.log('entry')",
		"assets/entry-TEST01.css": "/* entry */",
		"assets/page-TEST02.js":   "// console.log('page')",
	}
	return writeClientDir(t, manifest, files)
}

// newAssetApp builds the production wiring over a fixture client dir:
// a minimal error handler (404 status only), the static asset mount,
// and — to prove assets never leak into page responses — a catch-all
// shell-shaped handler. The real render package cannot be imported here
// (it imports httpserver); the shell shape is inlined for these tests.
func newAssetApp(t *testing.T) *fiber.App {
	t.Helper()
	dir := fixtureClientDir(t)
	assets, err := LoadAssets(dir)
	if err != nil {
		t.Fatalf("LoadAssets: %v", err)
	}
	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return c.Status(code).SendString("err:" + itoa(code))
		},
	})
	app.Get("/dashboard", func(c fiber.Ctx) error {
		return c.Type("html", "utf-8").SendString(
			`<div id="app" data-go-route="/dashboard" data-go-route-id="dashboard/page.svelte">` +
				`</div>` + "\n" +
				`<script type="module" src="` + assets.EntryScriptURL + `"></script>`)
	})
	if err := MountAssets(app, dir); err != nil {
		t.Fatalf("MountAssets: %v", err)
	}
	return app
}

// TestMountAssetsServesEntry: GET on the manifest-referenced entry script
// returns 200 with JavaScript MIME type (E09 tests 1-2).
func TestMountAssetsServesEntry(t *testing.T) {
	app := newAssetApp(t)
	resp, err := app.Test(httptest.NewRequest("GET", "/assets/entry-TEST01.js", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("entry status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") &&
		!strings.HasPrefix(ct, "application/javascript") {
		t.Fatalf("entry content-type = %q, want JavaScript MIME", ct)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "entry") {
		t.Fatalf("entry body wrong: %q", b)
	}
}

// TestMountAssetsServesDynamicChunkAndCSS: a dynamic page chunk and the
// entry CSS serve with correct types (E09 tests 3-4).
func TestMountAssetsServesDynamicChunkAndCSS(t *testing.T) {
	app := newAssetApp(t)

	resp, err := app.Test(httptest.NewRequest("GET", "/assets/page-TEST02.js", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("chunk status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") &&
		!strings.HasPrefix(ct, "application/javascript") {
		t.Fatalf("chunk content-type = %q", ct)
	}

	cssResp, err := app.Test(httptest.NewRequest("GET", "/assets/entry-TEST01.css", nil))
	if err != nil {
		t.Fatal(err)
	}
	if cssResp.StatusCode != 200 {
		t.Fatalf("css status = %d, want 200", cssResp.StatusCode)
	}
	if ct := cssResp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("css content-type = %q", ct)
	}
}

// TestMountAssetsCacheHeaders: content-hashed assets get a long-lived
// Cache-Control.
func TestMountAssetsCacheHeaders(t *testing.T) {
	app := newAssetApp(t)
	resp, err := app.Test(httptest.NewRequest("GET", "/assets/entry-TEST01.js", nil))
	if err != nil {
		t.Fatal(err)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=31536000" {
		t.Fatalf("cache-control = %q, want immutable-year", cc)
	}
}

// TestMountAssetsMissing404: a missing asset under /assets/ returns a true
// 404 — not a shell, not a silent 200 (E09 test 5).
func TestMountAssetsMissing404(t *testing.T) {
	app := newAssetApp(t)
	resp, err := app.Test(httptest.NewRequest("GET", "/assets/entry-NOTBUILT.js", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("missing asset status = %d, want 404", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(b), `<div id="app"`) {
		t.Fatal("missing asset must not return a CSR shell")
	}
}

// TestMountAssetsBlocksTraversal: traversal attempts must not expose
// files outside dist/client/assets (E09 test 6).
func TestMountAssetsBlocksTraversal(t *testing.T) {
	app := newAssetApp(t)
	// A secret placed beside the assets dir simulates source files.
	dir := fixtureClientDir(t) // separate tree; mount already happened on its own dir

	for _, p := range []string{
		"/assets/../.vite/manifest.json",
		"/assets/%2e%2e/.vite/manifest.json",
		"/assets/..%2f.vite%2fmanifest.json",
		"/assets/../../go.mod",
		"/assets/..\\..\\go.mod",
	} {
		resp, err := app.Test(httptest.NewRequest("GET", p, nil))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		if strings.Contains(string(b), "frontend/entry.ts") || strings.Contains(string(b), "module go-meta") {
			t.Fatalf("%s leaked file contents", p)
		}
		if resp.StatusCode != 404 {
			t.Fatalf("%s status = %d, want 404", p, resp.StatusCode)
		}
	}
	_ = dir
}

// TestMountAssetsManifestNotPublic: the Vite manifest must not be reachable
// under the asset prefix (E09 test 7).
func TestMountAssetsManifestNotPublic(t *testing.T) {
	app := newAssetApp(t)
	for _, p := range []string{
		"/assets/.vite/manifest.json",
		"/.vite/manifest.json",
		"/assets/manifest.json",
	} {
		resp, err := app.Test(httptest.NewRequest("GET", p, nil))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode == 200 {
			t.Fatalf("%s exposed the manifest", p)
		}
	}
}

// TestMountAssetsHeadSupported: HEAD works like GET for cache revalidation.
func TestMountAssetsHeadSupported(t *testing.T) {
	app := newAssetApp(t)
	resp, err := app.Test(httptest.NewRequest("HEAD", "/assets/entry-TEST01.js", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("HEAD status = %d, want 200", resp.StatusCode)
	}
}

// TestMountAssetsMissingDirFails: startup fails explicitly without built
// assets rather than serving pages that cannot boot.
func TestMountAssetsMissingDirFails(t *testing.T) {
	app := fiber.New()
	if err := MountAssets(app, t.TempDir()); err == nil {
		t.Fatal("want error for missing assets directory")
	}
}

// itoa avoids importing strconv just for a test status code.
func itoa(n int) string {
	return strconv.Itoa(n)
}
