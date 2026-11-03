package routecontext_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestPublicPackageFromAnotherModule(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	module := "module example.test/consumer\n\ngo 1.26.3\n\nrequire go-meta v0.0.0\nreplace go-meta => " + strconv.Quote(root) + "\n"
	source := "package consumer\nimport (\"context\"; \"go-meta/routecontext\")\nfunc Current(ctx context.Context) (routecontext.Route, bool) { return routecontext.FromContext(ctx) }\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "consumer.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("external consumer: %v\n%s", err, output)
	}
}
