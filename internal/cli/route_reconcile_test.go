package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRouteManifestTracksOnlyDeclarations(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app")
	if err := os.MkdirAll(filepath.Join(app, "one"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(app, "one", "page.svelte"), "one")
	first, err := routeManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(app, "one", "component.svelte"), "ignored")
	writeFixture(t, filepath.Join(app, "one", "page.svelte"), "edited")
	same, err := routeManifest(root)
	if err != nil || same != first {
		t.Fatalf("content edit changed manifest: %q %v", same, err)
	}
	if err := os.Rename(filepath.Join(app, "one"), filepath.Join(app, "two")); err != nil {
		t.Fatal(err)
	}
	renamed, err := routeManifest(root)
	if err != nil || renamed == first {
		t.Fatalf("rename missed: %q %v", renamed, err)
	}
	if err := os.RemoveAll(filepath.Join(app, "two")); err != nil {
		t.Fatal(err)
	}
	empty, err := routeManifest(root)
	if err != nil || empty != "" {
		t.Fatalf("deletion missed: %q %v", empty, err)
	}
}

func TestRouteManifestTracksDynamicTemplComponents(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "app", "users", "[id]")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, "page.templ"), "page")
	before, err := routeManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, "card.templ"), "component")
	after, err := routeManifest(root)
	if err != nil || after == before {
		t.Fatalf("component addition missed: %q %v", after, err)
	}
	if err := os.Remove(filepath.Join(dir, "card.templ")); err != nil {
		t.Fatal(err)
	}
	again, err := routeManifest(root)
	if err != nil || again != before {
		t.Fatalf("component deletion missed: %q %v", again, err)
	}
}
