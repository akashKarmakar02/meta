package httpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevelopmentAssets(t *testing.T) {
	a, err := DevelopmentAssets("http://127.0.0.1:5173")
	if err != nil {
		t.Fatal(err)
	}
	if a.DevClientURL != "/_go_meta_vite/@vite/client" || a.EntryScriptURL != "/_go_meta_vite/frontend/entry.ts" {
		t.Fatalf("unexpected assets: %+v", a)
	}
	for _, origin := range []string{"https://example.com:5173", "http://evil.example:5173", "http://127.0.0.1:5173/path", "http://user@127.0.0.1:5173"} {
		if _, err := DevelopmentAssets(origin); err == nil || !strings.Contains(err.Error(), "Vite") {
			t.Errorf("accepted %q: %v", origin, err)
		}
	}
}

func TestDevelopmentGlobalStylesheet(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll("frontend", 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := DevelopmentAssets("http://127.0.0.1:5173", true); err == nil {
		t.Fatal("missing stylesheet accepted")
	}
	if err := os.WriteFile(filepath.Join("frontend", "app.css"), []byte("body {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	assets, err := DevelopmentAssets("http://127.0.0.1:5173", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets.EntryCSSURLs) != 1 || assets.EntryCSSURLs[0] != "/_go_meta_vite/frontend/app.css?direct" {
		t.Fatalf("CSS URLs: %v", assets.EntryCSSURLs)
	}
}
