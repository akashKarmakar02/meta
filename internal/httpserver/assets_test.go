package httpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeClientDir stages a minimal dist/client tree: a manifest plus the
// asset files it references.
func writeClientDir(t *testing.T, manifest string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".vite"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".vite", "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const validManifest = `{
  "frontend/entry.ts": {
    "file": "assets/entry-ABC123.js",
    "src": "frontend/entry.ts",
    "isEntry": true
  }
}`

// TestLoadAssetsResolvesEntry: a valid manifest resolves the correct entry
// filename as a serving URL (E08 test 1).
func TestLoadAssetsResolvesEntry(t *testing.T) {
	dir := writeClientDir(t, validManifest, map[string]string{
		"assets/entry-ABC123.js": "// entry",
	})
	assets, err := LoadAssets(dir)
	if err != nil {
		t.Fatalf("LoadAssets: %v", err)
	}
	if assets.EntryScriptURL != "/assets/entry-ABC123.js" {
		t.Errorf("EntryScriptURL = %q, want /assets/entry-ABC123.js", assets.EntryScriptURL)
	}
}

// TestLoadAssetsFollowsHashChange: when the manifest hash changes, the
// resolved URL changes with it (E08 test 4).
func TestLoadAssetsFollowsHashChange(t *testing.T) {
	dir := writeClientDir(t, strings.Replace(validManifest, "ABC123", "XYZ999", -1), map[string]string{
		"assets/entry-XYZ999.js": "// entry",
	})
	assets, err := LoadAssets(dir)
	if err != nil {
		t.Fatalf("LoadAssets: %v", err)
	}
	if assets.EntryScriptURL != "/assets/entry-XYZ999.js" {
		t.Errorf("EntryScriptURL = %q, want /assets/entry-XYZ999.js", assets.EntryScriptURL)
	}
}

// TestLoadAssetsMissingManifest: a missing manifest fails explicitly.
func TestLoadAssetsMissingManifest(t *testing.T) {
	if _, err := LoadAssets(t.TempDir()); err == nil {
		t.Fatal("want error for missing manifest, got nil")
	}
}

// TestLoadAssetsInvalidManifest: malformed JSON fails explicitly and the
// error names no internal filesystem detail beyond the manifest path.
func TestLoadAssetsInvalidManifest(t *testing.T) {
	dir := writeClientDir(t, `{not json`, map[string]string{
		"assets/entry-ABC123.js": "// entry",
	})
	_, err := LoadAssets(dir)
	if err == nil {
		t.Fatal("want error for invalid manifest JSON, got nil")
	}
	if !strings.Contains(err.Error(), ".vite/manifest.json") {
		t.Errorf("error should name the manifest: %v", err)
	}
}

// TestLoadAssetsMissingEntry: a manifest without the entry key fails.
func TestLoadAssetsMissingEntry(t *testing.T) {
	dir := writeClientDir(t, `{"other.js": {"file": "assets/other.js", "isEntry": true}}`, map[string]string{
		"assets/other.js": "// other",
	})
	_, err := LoadAssets(dir)
	if err == nil {
		t.Fatal("want error for missing frontend/entry.ts key, got nil")
	}
	if !strings.Contains(err.Error(), "frontend/entry.ts") {
		t.Errorf("error should name the entry key: %v", err)
	}
}

// TestLoadAssetsNotEntry: the entry key present but isEntry false fails.
func TestLoadAssetsNotEntry(t *testing.T) {
	dir := writeClientDir(t, `{"frontend/entry.ts": {"file": "assets/entry-A.js"}}`, map[string]string{
		"assets/entry-A.js": "// entry",
	})
	_, err := LoadAssets(dir)
	if err == nil {
		t.Fatal("want error for non-isEntry key, got nil")
	}
}

// TestLoadAssetsMissingFileOnDisk: a manifest referencing an absent file
// fails at load time, not in the browser.
func TestLoadAssetsMissingFileOnDisk(t *testing.T) {
	dir := writeClientDir(t, validManifest, nil)
	if _, err := LoadAssets(dir); err == nil {
		t.Fatal("want error for asset missing from disk, got nil")
	}
}

// TestLoadAssetsRejectsUnsafePaths: paths that could escape the asset
// root or inject markup are rejected (E08 test 6).
func TestLoadAssetsRejectsUnsafePaths(t *testing.T) {
	cases := map[string]string{
		"absolute path":     "/etc/passwd",
		"parent segment":    "assets/../../../etc/passwd",
		"backslash":         `assets\entry.js`,
		"newline injection": "assets/entry.js\nx",
		"carriage return":   "assets/entry.js\r",
		"control character": "assets/e\x00ntry.js",
	}
	for name, file := range cases {
		manifest := `{"frontend/entry.ts": {"file": ` + jsonQuote(file) + `, "isEntry": true}}`
		// Stage files only for paths that are writable; character-level
		// rejects happen before the disk existence check.
		files := map[string]string{}
		if !strings.ContainsAny(file, "\n\r\x00") {
			files[strings.ReplaceAll(file, "\\", "/")] = "x"
		}
		dir := writeClientDir(t, manifest, files)
		_, err := LoadAssets(dir)
		if err == nil {
			t.Errorf("%s: want rejection for %q, got nil", name, file)
		}
	}
}

// jsonQuote encodes s as a JSON string literal for fixture manifests.
func jsonQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString("\\u0000")
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// TestLoadAssetsEntryCSS: CSS entries resolve to stylesheet URLs in
// manifest order.
func TestLoadAssetsEntryCSS(t *testing.T) {
	manifest := `{
  "frontend/entry.ts": {
    "file": "assets/entry-A.js",
    "isEntry": true,
    "css": ["assets/entry-A.css", "assets/shared-B.css"]
  }
}`
	dir := writeClientDir(t, manifest, map[string]string{
		"assets/entry-A.js":   "// entry",
		"assets/entry-A.css":  "/* css */",
		"assets/shared-B.css": "/* css */",
	})
	assets, err := LoadAssets(dir)
	if err != nil {
		t.Fatalf("LoadAssets: %v", err)
	}
	want := []string{"/assets/entry-A.css", "/assets/shared-B.css"}
	if len(assets.EntryCSSURLs) != len(want) {
		t.Fatalf("CSS URLs = %v, want %v", assets.EntryCSSURLs, want)
	}
	for i := range want {
		if assets.EntryCSSURLs[i] != want[i] {
			t.Errorf("CSS[%d] = %q, want %q", i, assets.EntryCSSURLs[i], want[i])
		}
	}
}
