package cli

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDevelopmentProxyRoutesAndHeaders(t *testing.T) {
	var received string
	vite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.RequestURI + "|" + r.Header.Get("Cookie") + "|" + r.Header.Get("Authorization") + "|" + r.Host
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = io.WriteString(w, "vite")
	}))
	defer vite.Close()
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_meta/image" || r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/about", http.StatusFound)
			return
		}
		w.Header().Set("Set-Cookie", "session=ok; Path=/")
		_, _ = fmt.Fprintf(w, "app:%s:%s:%s:%s:%s", r.URL.RawQuery, r.Header.Get("Cookie"), r.Header.Get("X-Go-Web-Navigation"), r.Header.Get("X-Forwarded-For"), r.Header.Get("Authorization"))
	}))
	defer app.Close()
	a, _ := url.Parse(app.URL)
	v, _ := url.Parse(vite.URL)
	proxy := httptest.NewUnstartedServer(nil)
	port := fmt.Sprint(proxy.Listener.Addr().(*net.TCPAddr).Port)
	proxy.Config.Handler = devProxyHandler(port, a, v)
	proxy.Start()
	defer proxy.Close()
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest("GET", proxy.URL+"/_go_meta_vite/@id/x%20y?raw=a%2Fb&x=1", nil)
	req.Header.Set("Cookie", "secret=1")
	req.Header.Set("Authorization", "Bearer secret")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Cache-Control") != "no-cache" || received != "/_go_meta_vite/@id/x%20y?raw=a%2Fb&x=1|||"+v.Host {
		t.Fatalf("vite response %d %q", res.StatusCode, received)
	}
	req, _ = http.NewRequest("GET", proxy.URL+"/dashboard?x=1", nil)
	req.Header.Set("Cookie", "session=abc")
	req.Header.Set("Authorization", "Bearer app-token")
	req.Header.Set("X-Go-Web-Navigation", "1")
	req.Header.Set("X-Forwarded-For", "203.0.113.42")
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if string(body) != "app:x=1:session=abc:1:127.0.0.1:Bearer app-token" || res.Header.Get("Set-Cookie") != "session=ok; Path=/" {
		t.Fatalf("application response: %s %v", body, res.Header)
	}
	for _, path := range []string{"/missing", "/_meta/image", "/frontend/unknown"} {
		res, err = client.Get(proxy.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if path != "/frontend/unknown" && res.StatusCode != 404 {
			t.Errorf("%s: %d", path, res.StatusCode)
		}
	}
	res, err = client.Get(proxy.URL + "/redirect")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 302 || res.Header.Get("Location") != "/about" {
		t.Fatal(res.Status, res.Header)
	}
	req, _ = http.NewRequest("GET", proxy.URL+"/", nil)
	req.Host = "evil.example:" + port
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("unsafe host accepted", res.Status)
	}
	vite.Close()
	res, err = client.Get(proxy.URL + "/_go_meta_vite/@vite/client")
	if err != nil {
		t.Fatal(err)
	}
	msg, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 502 || !strings.Contains(string(msg), "Vite development server unavailable") {
		t.Fatal(res.Status, string(msg))
	}
}

func TestDevelopmentProxyWebSocket(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" || r.Header.Get("Sec-WebSocket-Protocol") != "vite-hmr" {
			http.Error(w, "bad upgrade", 400)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Protocol: vite-hmr\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(conn, conn)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	proxy := httptest.NewUnstartedServer(nil)
	port := fmt.Sprint(proxy.Listener.Addr().(*net.TCPAddr).Port)
	proxy.Config.Handler = devProxyHandler(port, target, target)
	proxy.Start()
	defer proxy.Close()
	conn, err := net.DialTimeout("tcp", proxy.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = fmt.Fprintf(conn, "GET /_go_meta_vite/?token=x HTTP/1.1\r\nHost: 127.0.0.1:%s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Protocol: vite-hmr\r\n\r\n", port)
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 101 {
		t.Fatal(response.Status)
	}
	_, _ = conn.Write([]byte("hello"))
	data := make([]byte, 5)
	if _, err := io.ReadFull(reader, data); err != nil || string(data) != "hello" {
		t.Fatalf("upgrade stream: %q %v", data, err)
	}
}

func TestPublicDevPort(t *testing.T) {
	for _, p := range []string{"nope", "0", "65536", "-1"} {
		t.Setenv("PORT", p)
		if _, err := publicDevPort(); err == nil {
			t.Errorf("accepted %q", p)
		}
	}
	t.Setenv("PORT", "1234")
	if got, err := publicDevPort(); err != nil || got != "1234" {
		t.Fatalf("port %q: %v", got, err)
	}
}

func TestDevProxyPortConflictAndShutdown(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := fmt.Sprint(occupied.Addr().(*net.TCPAddr).Port)
	t.Setenv("PORT", port)
	err = func() error {
		p, err := startDevProxy("http://127.0.0.1:5173", io.Discard)
		if p != nil {
			defer p.Close()
		}
		return err
	}()
	if err == nil || !strings.Contains(err.Error(), "cannot start public development server") || !strings.Contains(err.Error(), "address already in use") || !strings.Contains(err.Error(), "127.0.0.1:"+port) {
		t.Fatalf("occupied port: %v", err)
	}
	_ = occupied.Close()
	if _, err := startDevProxy("http://127.0.0.1:"+port, io.Discard); err == nil {
		t.Fatal("accepted identical public and Vite ports")
	}
	p, err := startDevProxy("http://127.0.0.1:5173", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, time.Second)
	if err != nil {
		p.Close()
		t.Fatal(err)
	}
	// The proxy must close even an idle accepted connection during shutdown.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		n := len(p.connections)
		p.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	p.Close()
	if _, err := os.Stat(p.socketDir); !os.IsNotExist(err) {
		t.Fatalf("private socket directory remains: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("connection remained open")
	}
	_ = conn.Close()
}
