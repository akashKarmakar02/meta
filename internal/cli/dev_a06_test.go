package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestDevOutputAttribution covers A06 §9: messages carry their source
// tag, whole lines never interleave, and multiline compiler diagnostics
// keep one tag per line without truncation.
func TestDevOutputAttribution(t *testing.T) {
	var buf bytes.Buffer
	out := newDevOutput(&buf)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		w := out.air()
		io.WriteString(w, "building...\n")
		io.WriteString(w, "main.go:12:5: undefined: x\nmain.go:13:1: second diagnostic\n")
	}()
	go func() {
		defer wg.Done()
		w := out.vite()
		io.WriteString(w, "VITE v8.3.5 ready\n")
	}()
	wg.Wait()

	got := buf.String()
	for _, want := range []string{
		"[air] building...",
		"[air] main.go:12:5: undefined: x",
		"[air] main.go:13:1: second diagnostic",
		"[vite] VITE v8.3.5 ready",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if !strings.HasPrefix(line, "[air] ") && !strings.HasPrefix(line, "[vite] ") {
			t.Errorf("interleaved or untagged line %q", line)
		}
	}
}

// TestDevOutputPartialLine covers a write split across chunk boundaries:
// the pieces concatenate into one tagged line once the newline arrives,
// and nothing is duplicated.
func TestDevOutputPartialLine(t *testing.T) {
	var buf bytes.Buffer
	out := newDevOutput(&buf)
	w := out.air()
	io.WriteString(w, "building") // partial: no newline yet
	if strings.Contains(buf.String(), "\n") {
		t.Errorf("partial write flushed early: %q", buf.String())
	}
	io.WriteString(w, "...\n") // completes the line
	if !strings.Contains(buf.String(), "[air] building...\n") {
		t.Errorf("split line lost content: %q", buf.String())
	}
}

// TestWaitForDevReadyReady verifies the success path: once both probes
// answer 200, readiness returns promptly (no fixed sleeps as signals).
func TestWaitForDevReadyReady(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(200)
	}))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	start := time.Now()
	if err := waitForDevReady(context.Background(), up.URL, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("readiness took %s despite both services up", d)
	}
	_ = u
}

// TestWaitForDevReadyTimeout verifies a service that never comes up fails
// within the bounded deadline and names the service in the error.
func TestWaitForDevReadyTimeout(t *testing.T) {
	// A server that only answers /healthz; the Vite probe 404s forever.
	half := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
	}))
	defer half.Close()
	err := waitForDevReady(context.Background(), half.URL, 900*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "Vite development server did not become ready") {
		t.Fatalf("want Vite-not-ready error, got %v", err)
	}
}

// TestWaitForDevReadyCancel verifies Ctrl+C during startup aborts the
// wait instead of hanging until the deadline.
func TestWaitForDevReadyCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	unreachable := "http://127.0.0.1:1" // nothing listens
	errc := make(chan error, 1)
	go func() { errc <- waitForDevReady(ctx, unreachable, 30*time.Second) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-errc:
		if err != context.Canceled {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel during readiness did not abort")
	}
}

// TestCheckTemplTool covers the go.mod tool-directive check.
func TestCheckTemplTool(t *testing.T) {
	root := t.TempDir()
	// Without the directive.
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := checkTemplTool(root)
	if err == nil || !strings.Contains(err.Error(), "templ") || !strings.Contains(err.Error(), "go get -tool") {
		t.Fatalf("want templ-directive error, got %v", err)
	}
	// With the directive.
	gomod := "module x\n\ngo 1.26\n\ntool github.com/a-h/templ/cmd/templ\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkTemplTool(root); err != nil {
		t.Fatalf("with directive: %v", err)
	}
}

// TestCheckNodeModulesHint verifies the missing-node_modules failure names
// the dependency and the recovery command.
func TestCheckNodeModulesHint(t *testing.T) {
	root := t.TempDir()
	err := checkNodeModules(root)
	if err == nil || !strings.Contains(err.Error(), "node_modules") || !strings.Contains(err.Error(), "npm ci") || !strings.Contains(err.Error(), root) {
		t.Fatalf("want actionable npm-ci hint, got %v", err)
	}
	makeNodeModules(t, root)
	if err := checkNodeModules(root); err != nil {
		t.Fatalf("installed: %v", err)
	}
}

// TestCheckVitePortFreeOccupied verifies an occupied Vite port fails with
// the named service before any process spawns.
func TestCheckVitePortFreeOccupied(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := fmt.Sprint(occupied.Addr().(*net.TCPAddr).Port)
	err = checkVitePortFree(port)
	_ = occupied.Close()
	if err == nil || !strings.Contains(err.Error(), "cannot start Vite development server") || !strings.Contains(err.Error(), "address already in use") || !strings.Contains(err.Error(), "GO_META_VITE_PORT") {
		t.Fatalf("occupied Vite port: %v", err)
	}
}

// TestCheckVitePortFreeAvailable verifies a genuinely free port passes.
func TestCheckVitePortFreeAvailable(t *testing.T) {
	// Reserve, read the port, release, then check: no TOCTOU window for
	// another process to grab it in between is fully avoidable, but the
	// reservation-releasetest keeps it tiny.
	reserve, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := fmt.Sprint(reserve.Addr().(*net.TCPAddr).Port)
	_ = reserve.Close()
	if err := checkVitePortFree(port); err != nil {
		t.Fatalf("free port rejected: %v", err)
	}
}
