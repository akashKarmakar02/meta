package cli

import (
	"bytes"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestDevAirIntegration is the real-Air end-to-end verification of P2-A02,
// run only on Linux and only when an air executable is available (PATH or
// $GOPATH/bin). Everything else in this package tests through fakes.
//
// It exercises the full contract on a temporary fixture project:
//  1. meta dev launches Air and the Go backend becomes reachable;
//  2. editing a Go source file rebuilds and restarts the backend;
//  3. the changed behavior becomes observable over HTTP;
//  4. SIGINT stops meta dev and leaves no orphaned server process.
//
// Requires: air, go, network permission for localhost binds, and fsnotify
// support (inotify) for the watcher. Skips with an explicit message
// otherwise; never fails for a missing optional tool.
func TestDevAirIntegration(t *testing.T) {
	if os.Getenv("GO_META_SKIP_AIR_INTEGRATION") == "1" {
		t.Skip("GO_META_SKIP_AIR_INTEGRATION=1")
	}
	if _, err := exec.LookPath("air"); err != nil {
		if gobin := goBinDir(); gobin != "" {
			if _, statErr := os.Stat(filepath.Join(gobin, "air")); statErr != nil {
				t.Skipf("air not found in PATH or %s; install with: go install github.com/air-verse/air@latest", gobin)
			}
		} else {
			t.Skip("air not found in PATH; install with: go install github.com/air-verse/air@latest")
		}
	}

	root := newAirFixtureProject(t)
	t.Chdir(root) // root discovery must resolve to the fixture, not this repo
	port := freeTCPPort(t)
	t.Setenv("PORT", port)

	// Run meta dev's core directly (same process, real execRunner), so the
	// test can interrupt it via the injected signal channel instead of
	// wrangling a second process tree.
	sigs := make(chan os.Signal, 1)
	var stdout, stderr bytes.Buffer
	errc := make(chan error, 1)
	go func() {
		errc <- runDevWith(&Context{Stdout: &stdout, Stderr: &stderr}, devDeps{
			lookAir: airLookPath,
			runner:  execRunner{},
			signals: func() (<-chan os.Signal, func()) { return sigs, func() {} },
			grace:   airShutdownGrace,
		})
	}()

	// 1. Backend becomes reachable: poll /healthz with a bounded deadline.
	base := "http://127.0.0.1:" + port
	waitForBody(t, base+"/healthz", "ok", "initial backend start")

	// 2. Change a Go source file safely, inside the fixture only.
	handlerFile := filepath.Join(root, "cmd", "server", "main.go")
	original := readFixture(t, handlerFile)
	edited := strings.Replace(original, `const revision = "0"`, `const revision = "1"`, 1)
	if edited == original {
		t.Fatalf("fixture marker revision=%q not found in %s", "0", handlerFile)
	}
	writeFixture(t, handlerFile, edited)

	// 3. Rebuild + restart observed: the revision endpoint flips 0 -> 1.
	waitForBody(t, base+"/revision", "revision=1", "backend rebuild after Go source edit")

	// 4. Restore the original source; the tree must end clean.
	writeFixture(t, handlerFile, original)

	// 5. SIGINT stops the session cleanly and leaves no orphan.
	sigs <- syscall.SIGINT
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("interrupted session ended with error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("meta dev did not return after SIGINT")
	}

	// The server must be gone; a bounded poll for connection refusal.
	waitForDown(t, base+"/healthz", "backend after SIGINT")
}

// waitForBody polls url until it returns status 200 whose body contains
// want, failing after a bounded deadline. No fixed sleeps as readiness
// signals.
func waitForBody(t *testing.T, url, want, what string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			body := new(bytes.Buffer)
			_, _ = body.ReadFrom(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && strings.Contains(body.String(), want) {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s: GET %s never returned %q", what, url, want)
}

// waitForDown polls url until connections are refused, proving the
// backend process has terminated.
func waitForDown(t *testing.T, url, what string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err != nil {
			return // connection refused: server gone
		}
		_ = resp.Body.Close()
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("%s still reachable at %s after shutdown", what, url)
}

// freeTCPPort picks an unused TCP port by binding :0 and reading the
// chosen port, then releasing it before Air binds the same address.
func freeTCPPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split port: %v", err)
	}
	return port
}

// newAirFixtureProject builds a temporary, self-contained go-meta-shaped
// project whose backend is a tiny net/http server that reports its code
// revision from an env var. Editing the env var in source simulates a
// normal Go edit for the watcher.
func newAirFixtureProject(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "fixture-app")
	for _, d := range []string{"app", "cmd/server", "dist/client/.vite"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"go.mod": "module fixture.app\n\ngo 1.26\n",
		// Minimal go-meta marker so root discovery accepts the fixture.
		"app/.keep": "",
		// The server: revision endpoint lets the test observe rebuilds.
		"cmd/server/main.go": `package main

import (
	"fmt"
	"net/http"
	"os"
)

// revision is edited by the test to prove Go-source changes rebuild and
// restart the backend.
const revision = "0"

func main() {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	})
	http.HandleFunc("/revision", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "revision=%s", revision)
	})
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	_ = http.ListenAndServe("127.0.0.1:"+port, nil)
}
`,
		// The Air config watches cmd/server and app, excludes everything
		// transient.
		".air.toml": `root = "."
tmp_dir = ".tmp/air"

[build]
  cmd = "go build -o ./.tmp/air/server ./cmd/server"
  entrypoint = ["./.tmp/air/server"]
  include_ext = ["go"]
  exclude_dir = ["node_modules", "dist", ".tmp", ".git"]
  exclude_regex = ["_test.go"]
  delay = 500

[misc]
  clean_on_exit = true
`,
		// A bundle manifest so checkClientAssets passes without a real Vite
		// build.
		"dist/client/.vite/manifest.json": "{}",
	}
	for name, content := range files {
		full := filepath.Join(root, name)
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	return root
}

func readFixture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
