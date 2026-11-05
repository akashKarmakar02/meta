package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akashKarmakar02/meta/internal/routing"
)

func TestProjectImports(t *testing.T) {
	root := t.TempDir()
	mod := filepath.Join(root, "go.mod")
	if err := os.WriteFile(mod, []byte("module example.com/customer/site\n\ngo 1.26.10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modulePath, appDir, err := ProjectImports(filepath.Join(root, "src", "app"), root)
	if err != nil {
		t.Fatal(err)
	}
	if modulePath != "example.com/customer/site" || appDir != "src/app" {
		t.Fatalf("got %q %q", modulePath, appDir)
	}
	if _, _, err := ProjectImports(filepath.Join(root, "..", "other"), root); err == nil {
		t.Fatal("accepted app outside module root")
	}
	if _, _, err := ProjectImports(root, root); err == nil {
		t.Fatal("accepted module root as app")
	}
	if err := os.WriteFile(mod, []byte("go 1.26.10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ProjectImports(filepath.Join(root, "app"), root); err == nil || !strings.Contains(err.Error(), "module directive") {
		t.Fatalf("missing module: %v", err)
	}
	if err := os.WriteFile(mod, []byte("module example.com/../bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ProjectImports(filepath.Join(root, "app"), root); err == nil {
		t.Fatal("accepted invalid module path")
	}
	if err := os.Remove(mod); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ProjectImports(filepath.Join(root, "app"), root); err == nil {
		t.Fatal("accepted missing go.mod")
	}
}

func TestProjectImportsInGoAndClientRegistries(t *testing.T) {
	manifest := routing.NewManifest([]routing.Route{
		{ID: "page.templ", Path: "/", SourceFile: "page.templ", Mode: routing.ModeSSR,
			Layouts: []routing.Layout{{ID: "app/layout.templ", SourceFile: "layout.templ", Mode: routing.ModeSSR}}},
		{ID: "dashboard/page.svelte", Path: "/dashboard", SourceFile: "dashboard/page.svelte", Mode: routing.ModeCSR,
			Layouts: []routing.Layout{{ID: "app/dashboard/layout.svelte", SourceFile: "dashboard/layout.svelte", Mode: routing.ModeCSR}}},
	})
	goSource, err := RoutesFileForProject(manifest, "example.org/shop", "src/app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goSource), `"example.org/shop/src/app"`) {
		t.Fatalf("Go imports: %s", goSource)
	}
	client := RouteRegistryTSForProject(manifest, "src/app")
	for _, want := range []string{`"../src/app/dashboard/page.svelte"`, `"../src/app/dashboard/layout.svelte"`} {
		if !strings.Contains(client, want) {
			t.Errorf("client imports missing %s: %s", want, client)
		}
	}
}
