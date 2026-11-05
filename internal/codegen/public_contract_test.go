package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/akashKarmakar02/meta/internal/routing"
)

// A CSR-only manifest avoids project-specific page imports, which are handled
// by the separate project-aware generator work. This tests the real generated
// route contract from outside the framework module.
func TestGeneratedRoutesUsePublicContractInExternalModule(t *testing.T) {
	manifest := routing.NewManifest([]routing.Route{{
		ID: "dashboard/page.svelte", Path: "/dashboard", SourceFile: "dashboard/page.svelte",
		Mode:    routing.ModeCSR,
		Layouts: []routing.Layout{{ID: "app/layout.svelte", SourceFile: "layout.svelte", Mode: routing.ModeCSR}},
	}})
	src, err := RoutesFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "github.com/akashKarmakar02/meta/internal/") {
		t.Fatal("generated routes import framework internals")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                  "module example.test/consumer\n\ngo 1.26.10\n\nrequire github.com/akashKarmakar02/meta v0.0.0\nreplace github.com/akashKarmakar02/meta => " + strconv.Quote(root) + "\n",
		"generated/routes_gen.go": string(src),
		"contract_test.go": `package consumer
import (
 "encoding/json"
 "testing"
 routes "example.test/consumer/generated"
 "github.com/akashKarmakar02/meta/routing"
)
func TestGeneratedContract(t *testing.T) {
 var r routing.Route = routes.RegisterRoutes()[0]
 if r.ID != "dashboard/page.svelte" || r.Path != "/dashboard" || r.Mode != routing.ModeCSR || len(r.Layouts) != 1 || r.Layouts[0].Mode != routing.ModeCSR { t.Fatalf("unexpected route: %+v", r) }
 got, err := json.Marshal(r)
 if err != nil || string(got) != "{\"id\":\"dashboard/page.svelte\",\"path\":\"/dashboard\",\"sourceFile\":\"dashboard/page.svelte\",\"mode\":\"csr\",\"layouts\":[{\"id\":\"app/layout.svelte\",\"sourceFile\":\"layout.svelte\",\"mode\":\"csr\"}]}" { t.Fatalf("JSON %s: %v", got, err) }
}
`,
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "-mod=mod", "./...")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("external consumer: %v\n%s", err, output)
	}
}
