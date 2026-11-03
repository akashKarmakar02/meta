package cli

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRouteReconciliationAir runs the real CLI, generator, Air and HTTP server
// in a private copy of the example project; it never edits the user's app.
func TestRouteReconciliationAir(t *testing.T) {
	if os.Getenv("GO_META_SKIP_AIR_INTEGRATION") == "1" {
		t.Skip("GO_META_SKIP_AIR_INTEGRATION=1")
	}
	if _, err := airLookPath(); err != nil {
		t.Skip(err)
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nodeLookPath(); err != nil {
		t.Skip(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "node_modules", "vite", "bin", "vite.js")); err != nil {
		t.Skip("Vite dependencies unavailable; run npm ci")
	}
	root := t.TempDir()
	for _, name := range []string{".air.toml", "go.mod", "go.sum", "package.json", "vite.config.ts", "app", "cmd", "internal", "frontend", "generated", "routeparams", "routecontext", "loader", "loaderdata"} {
		cmd := exec.Command("cp", "-a", filepath.Join(repo, name), root)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("copy %s: %v: %s", name, err, out)
		}
	}
	if err := os.Symlink(filepath.Join(repo, "node_modules"), filepath.Join(root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "dist/client/.vite"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "dist/client/assets"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(root, "dist/client/.vite/manifest.json"), `{"frontend/entry.ts":{"file":"assets/entry.js","isEntry":true}}`)
	writeFixture(t, filepath.Join(root, "dist/client/assets/entry.js"), "")
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", "bin/meta", "./cmd/meta")
	build.Dir = root
	build.Env = append(os.Environ(), "GOFLAGS=-buildvcs=false")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build meta: %v: %s", err, out)
	}
	log, err := os.Create(filepath.Join(t.TempDir(), "air.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	port := freeTCPPort(t)
	cmd := exec.Command(filepath.Join(root, "bin/meta"), "dev")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PORT="+port, "GO_META_VITE_PORT="+freeTCPPort(t), "GOFLAGS=-buildvcs=false")
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stopped := false
	defer func() {
		if !stopped {
			_ = cmd.Process.Signal(syscall.SIGINT)
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				_ = cmd.Process.Kill()
				<-done
			}
		}
		if t.Failed() {
			data, _ := os.ReadFile(log.Name())
			t.Logf("Air output:\n%s", data)
		}
	}()
	base := "http://127.0.0.1:" + port
	waitRoute := func(path string, status int, body string) {
		t.Helper()
		deadline := time.Now().Add(90 * time.Second)
		client := &http.Client{Timeout: time.Second}
		for time.Now().Before(deadline) {
			select {
			case err := <-done:
				stopped = true
				t.Fatalf("meta dev exited early: %v", err)
			default:
			}
			resp, err := client.Get(base + path)
			if err == nil {
				data, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode == status && strings.Contains(string(data), body) {
					return
				}
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s status %d containing %q", path, status, body)
	}
	waitRoute("/", 200, "")
	dir := filepath.Join(root, "app", "p2reconcile")
	create := func() {
		t.Helper()
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(dir, "page.templ"), "package p2reconcile\n\ntempl P2reconcile() {\n <main>route-test</main>\n}\n\ntempl P2renamed() {\n <main>route-test</main>\n}\n")
	}
	create()
	waitRoute("/p2reconcile", 200, "route-test")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	waitRoute("/p2reconcile", 404, "")
	create()
	waitRoute("/p2reconcile", 200, "route-test")
	moved := filepath.Join(root, "app", "p2renamed")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	waitRoute("/p2renamed", 200, "route-test")
	waitRoute("/p2reconcile", 404, "")
	if err := os.Remove(filepath.Join(moved, "page.templ")); err != nil {
		t.Fatal(err)
	}
	waitRoute("/p2renamed", 404, "")
	// A nested layout must give way to the root layout when its file is removed.
	create()
	writeFixture(t, filepath.Join(dir, "layout.templ"), "package p2reconcile\n\ntempl P2reconcileLayout(pageContent templ.Component) {\n <div data-test-layout=\"nearest\">@pageContent</div>\n}\n")
	waitRoute("/p2reconcile", 200, "nearest")
	if err := os.Remove(filepath.Join(dir, "layout.templ")); err != nil {
		t.Fatal(err)
	}
	waitRoute("/p2reconcile", 200, "data-go-site-nav")
	csr := filepath.Join(root, "app", "p2csr")
	if err := os.MkdirAll(csr, 0755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(csr, "page.svelte"), "<p>page</p>")
	writeFixture(t, filepath.Join(csr, "layout.svelte"), "<slot />")
	// The navigation envelope exposes the selected Svelte layout ID.
	deadline := time.Now().Add(90 * time.Second)
	for {
		req, _ := http.NewRequest("GET", base+"/p2csr", nil)
		req.Header.Set("X-Go-Web-Navigation", "1")
		resp, err := (&http.Client{Timeout: time.Second}).Do(req)
		if err == nil {
			data, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == 200 && strings.Contains(string(data), "app/p2csr/layout.svelte") {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("Svelte layout did not become active")
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err := os.Remove(filepath.Join(csr, "layout.svelte")); err != nil {
		t.Fatal(err)
	}
	waitRoute("/p2csr", 200, "data-go-site-nav")
	if err := os.Remove(filepath.Join(csr, "page.svelte")); err != nil {
		t.Fatal(err)
	}
	waitRoute("/p2csr", 404, "")
	// Cross a reconciliation boundary while adding/removing routes quickly.
	for i := 0; i < 4; i++ {
		rapid := filepath.Join(root, "app", fmt.Sprintf("p2rapid%d", i))
		if err := os.MkdirAll(rapid, 0755); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(rapid, "page.svelte"), "<p>rapid</p>")
		if err := os.RemoveAll(rapid); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(8 * time.Second)
	before, _ := os.ReadFile(log.Name())
	time.Sleep(6 * time.Second)
	after, _ := os.ReadFile(log.Name())
	if bytes.Count(after, []byte("building...")) != bytes.Count(before, []byte("building...")) {
		t.Fatal("Air rebuilt while route tree was idle")
	}
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("meta dev shutdown: %v", err)
		}
		stopped = true
	case <-time.After(30 * time.Second):
		t.Fatal("meta dev did not stop")
	}
	waitForDown(t, base+"/", "backend after reconciliation session")
	if runtime.GOOS == "linux" {
		entries, err := os.ReadDir("/proc")
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			cwd, err := os.Readlink(filepath.Join("/proc", entry.Name(), "cwd"))
			if err == nil && cwd == root {
				t.Errorf("process %s still running in fixture after shutdown", entry.Name())
			}
		}
	}
}
