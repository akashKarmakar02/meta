package codegen

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akashKarmakar02/meta/internal/routing"
)

func TestDynamicPackagesCompileAndReconcile(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		name = filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"go.mod", "go.sum", "internal/routing/schema.go"} {
		data, err := os.ReadFile(filepath.Join("../..", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		write(name, string(data))
	}
	write("app/users/helpers/helper.go", "package helpers\nfunc Text() string { return \"helper\" }\n")
	write("app/users/[userId]/posts/[postId]/page.templ", "package postpage\nimport \"github.com/akashKarmakar02/meta/app/users/helpers\"\ntempl Page() { <main>@Card(helpers.Text())</main> }\n")
	write("app/users/[userId]/posts/[postId]/card.templ", "package postpage\ntempl Card(s string) { <p>{ s }</p> }\n")
	write("app/users/[userId]/posts/[postId]/layout.templ", "package postpage\ntempl Layout(c templ.Component) { <section>@c</section> }\n")
	fsys := os.DirFS(filepath.Join(root, "app"))
	routes, err := routing.Discover(fsys)
	if err != nil {
		t.Fatal(err)
	}
	manifest := routing.NewManifest(routes)
	if err := VerifySymbols(fsys, manifest); err != nil {
		t.Fatal(err)
	}
	ids, err := GenerateDynamicPackages(filepath.Join(root, "app"), root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("package IDs: %v", ids)
	}
	if err := WriteRoutesFile(root, manifest); err != nil {
		t.Fatal(err)
	}
	write("cmd/proof/main.go", "package main\nimport (\"context\"; \"os\"; r \"github.com/akashKarmakar02/meta/generated\")\nfunc main(){ c,e:=r.SSRPage(r.RegisterRoutes()[0]); if e!=nil { panic(e) }; if e=c.Render(context.Background(),os.Stdout); e!=nil { panic(e) } }\n")
	cmd := exec.Command("go", "run", "-buildvcs=false", "./cmd/proof")
	cmd.Dir = root
	got, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compile and run: %v\n%s", err, got)
	}
	if string(got) != "<section><main><p>helper</p></main></section>" {
		t.Fatalf("render: %s", got)
	}
	original := filepath.Join(root, "app/users/[userId]/posts/[postId]")
	entries, err := os.ReadDir(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") {
			t.Fatalf("unexpected Go source %s", e.Name())
		}
	}
	generated := filepath.Join(root, "generated", "routepages", ids[0], "page_templ.go")
	before, err := os.ReadFile(generated)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(generated)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	// The normal templ pass must not remove mirrored output as an orphan.
	cmd = exec.Command("go", "tool", "templ", "generate", "-ignore-pattern", `[/\\]app[/\\].*\[|[/\\]generated[/\\]routepages[/\\]`)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("normal templ pass: %v\n%s", err, output)
	}
	if _, err := GenerateDynamicPackages(filepath.Join(root, "app"), root, manifest); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(generated)
	if err != nil {
		t.Fatal(err)
	}
	next, err := os.Stat(generated)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || !info.ModTime().Equal(next.ModTime()) {
		t.Fatal("unchanged output was rewritten")
	}
	if err := CleanDynamicPackages(root, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(generated)); !os.IsNotExist(err) {
		t.Fatalf("stale package remains: %v", err)
	}
}

func TestDynamicPackageIdentity(t *testing.T) {
	seen := map[string]string{}
	for _, path := range []string{"users/id", "users/[id]", "users/[ID]", "users/[iD]"} {
		id := dynamicPackageID(path)
		if prev, ok := seen[id]; ok {
			t.Fatalf("%s and %s collide", prev, path)
		}
		seen[id] = path
		if id != dynamicPackageID(path) {
			t.Fatal("unstable identity")
		}
	}
}
