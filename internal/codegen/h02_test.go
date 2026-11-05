package codegen

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/akashKarmakar02/meta/internal/routing"
)

// TestApplicationRegeneration verifies both artifacts against the same real
// app manifest and against a second independent generation. make generate
// precedes the test suite; Go and TypeScript compilation are checked by the
// normal build and check targets.
func TestApplicationRegeneration(t *testing.T) {
	root := filepath.Join("..", "..")
	fsys := os.DirFS(filepath.Join(root, "app"))
	routes, err := routing.Discover(fsys)
	if err != nil {
		t.Fatalf("discover application routes: %v", err)
	}
	manifest := routing.NewManifest(routes)
	if err := VerifySymbols(fsys, manifest); err != nil {
		t.Fatalf("verify application symbols: %v", err)
	}

	artifacts := []string{
		filepath.Join("generated", "routes_gen.go"),
		filepath.Join("frontend", "route-registry.generated.ts"),
	}
	var first [][]byte
	for iteration := range 2 {
		dir := t.TempDir()
		if err := WriteRoutesFile(dir, manifest); err != nil {
			t.Fatalf("generation %d Go: %v", iteration, err)
		}
		if err := WriteRouteRegistryTS(dir, manifest); err != nil {
			t.Fatalf("generation %d TypeScript: %v", iteration, err)
		}
		for i, artifact := range artifacts {
			got, err := os.ReadFile(filepath.Join(dir, artifact))
			if err != nil {
				t.Fatalf("read generated %s: %v", artifact, err)
			}
			if iteration == 0 {
				first = append(first, got)
				current, err := os.ReadFile(filepath.Join(root, artifact))
				if err != nil {
					t.Fatalf("read application %s (run make generate): %v", artifact, err)
				}
				if !bytes.Equal(got, current) {
					t.Errorf("%s differs from regenerated source; run make generate", artifact)
				}
			} else if !bytes.Equal(got, first[i]) {
				t.Errorf("%s changed on repeated generation", artifact)
			}
		}
	}
}

func TestVerifySymbolsRejectsMisnamedLayout(t *testing.T) {
	fsys := fstest.MapFS{
		"page.templ":   {Data: []byte("templ Page() {}")},
		"layout.templ": {Data: []byte("templ Wrong(templ.Component) {}")},
	}
	routes, err := routing.Discover(fsys)
	if err != nil {
		t.Fatalf("discover fixture: %v", err)
	}
	err = VerifySymbols(fsys, routing.NewManifest(routes))
	if err == nil {
		t.Fatal("expected a missing layout symbol error")
	}
	for _, want := range []string{"layout.templ", "Layout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}
