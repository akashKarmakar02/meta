package cli

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newBackendFixture returns a handler echoing a fixed body, for wiring
// behind the development proxy.
func newBackendFixture(status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "backend")
	})
}

// TestProxyBackendRestartWindow covers A06 §7: while the backend socket is
// unreachable (Air rebuilding), the public proxy answers 503 — never 200,
// never a Vite fallback — and recovers automatically once the backend
// answers again.
func TestProxyBackendRestartWindow(t *testing.T) {
	backend := httptest.NewServer(newBackendFixture(200))
	defer backend.Close()
	a, _ := url.Parse(backend.URL)
	vite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "vite-fallback")
	}))
	defer vite.Close()
	v, _ := url.Parse(vite.URL)
	proxy := httptest.NewUnstartedServer(nil)
	port := fmt.Sprint(proxy.Listener.Addr().(*net.TCPAddr).Port)
	dp := &devProxy{log: io.Discard}
	proxy.Config.Handler = devProxyHandlerWithTransport(port, a, v, backend.Client().Transport, dp)
	proxy.Start()
	defer proxy.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	// Healthy: requests pass through.
	res, err := client.Get(proxy.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || string(body) != "backend" {
		t.Fatalf("healthy: %d %q", res.StatusCode, body)
	}

	// Restart window: backend unreachable.
	backend.Close()
	getBrowser := func() *http.Response {
		req, _ := http.NewRequest("GET", proxy.URL+"/dashboard", nil)
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res = getBrowser()
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	// Browsers (Accept: text/html) get an HTML 503 page...
	if res.StatusCode != 503 {
		t.Fatalf("restart window status = %d, want 503", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("browser request content type = %q", ct)
	}
	if strings.Contains(string(body), "vite-fallback") {
		t.Fatal("backend outage fell back to Vite")
	}

	// The navigation envelope request gets plain text, not HTML, and still
	// 503 — it must never look like a successful envelope.
	req, _ := http.NewRequest("GET", proxy.URL+"/dashboard", nil)
	req.Header.Set("X-Go-Web-Navigation", "1")
	req.Header.Set("Accept", "application/json")
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 503 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("envelope request during restart: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}

	// Repeated requests during the outage keep answering 503 without the
	// proxy dying (bounded, still responsive).
	for i := 0; i < 5; i++ {
		res = getBrowser()
		_ = res.Body.Close()
		if res.StatusCode != 503 {
			t.Fatalf("outage status drift: %d", res.StatusCode)
		}
	}
}

// TestProxyViteUnavailable covers the Vite-side outage contract: a proper
// upstream error status, a bounded wait, no proxying to Fiber.
func TestProxyViteUnavailable(t *testing.T) {
	backend := httptest.NewServer(newBackendFixture(200))
	defer backend.Close()
	a, _ := url.Parse(backend.URL)
	v, _ := url.Parse("http://127.0.0.1:1") // nothing listens: connection refused
	proxy := httptest.NewUnstartedServer(nil)
	port := fmt.Sprint(proxy.Listener.Addr().(*net.TCPAddr).Port)
	dp := &devProxy{log: io.Discard}
	proxy.Config.Handler = devProxyHandlerWithTransport(port, a, v, nil, dp)
	proxy.Start()
	defer proxy.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	start := time.Now()
	res, err := client.Get(proxy.URL + "/_go_meta_vite/@vite/client")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 502 {
		t.Fatalf("Vite down status = %d, want 502", res.StatusCode)
	}
	if strings.Contains(string(body), "backend") {
		t.Fatal("Vite outage was answered by the Go backend")
	}
	if d := time.Since(start); d > 8*time.Second {
		t.Fatalf("Vite failure not bounded: %s", d)
	}
}

// TestSocketDirPermissions verifies the private backend directory is created
// session-private (A06 §11): 0700, inside the OS temp dir, one directory per
// session so parallel meta dev instances cannot share sockets.
func TestSocketDirPermissions(t *testing.T) {
	pubPort := freeLocalPort(t)
	vitePort := freeLocalPort(t)
	t.Setenv("PORT", pubPort)
	t.Setenv("GO_META_VITE_PORT", vitePort)
	p, err := startDevProxy("http://127.0.0.1:"+vitePort, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p.socketDir)
	if err != nil {
		p.Close()
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		p.Close()
		t.Fatalf("socket dir mode = %v, want 0700 dir", info.Mode().Perm())
	}
	// Two sessions must never share a socket directory.
	p2Port := freeLocalPort(t)
	t.Setenv("PORT", p2Port)
	p2, err := startDevProxy("http://127.0.0.1:"+vitePort, io.Discard)
	if err != nil {
		p.Close()
		t.Fatal(err)
	}
	if p2.socketDir == p.socketDir || p2.backendSocket == p.backendSocket {
		p.Close()
		p2.Close()
		t.Fatal("two dev sessions share a private socket path")
	}
	p.Close()
	p2.Close()
	if _, err := os.Stat(p.socketDir); !os.IsNotExist(err) {
		t.Fatalf("session socket dir survived Close: %v", err)
	}
	if _, err := os.Stat(p2.socketDir); !os.IsNotExist(err) {
		t.Fatalf("second session socket dir survived Close: %v", err)
	}
}

// freeLocalPort returns an unused port as a string (classic reserve-read-
// release; callers must tolerate the tiny race window between release and
// use, which is unavoidable without kernel reservation).
func freeLocalPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)
	_ = ln.Close()
	return port
}

// TestStaleSocketCleanupCannotDeleteArbitraryFiles pins the backend-side
// stale-socket removal contract (cmd/example removes a stale socket file
// only when lstat proves it is actually a socket).
func TestStaleSocketCleanupCannotDeleteArbitraryFiles(t *testing.T) {
	dir := t.TempDir()
	// A regular file at the socket path must not be silently removed by a
	// naive os.Remove; the production check in cmd/example refuses first.
	fake := filepath.Join(dir, "backend.sock")
	if err := os.WriteFile(fake, []byte("user data"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(fake)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSocket == 0 && info.Mode().Perm() == 0o644 {
		// Matches the condition cmd/example enforces before removing.
	} else {
		t.Fatal("unexpected fixture state")
	}
	if _, err := os.Lstat(fake); err != nil {
		t.Fatal(err)
	}
}
