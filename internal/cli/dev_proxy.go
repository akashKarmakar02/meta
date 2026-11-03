package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// vitePrefix is reserved exclusively for Vite's development module graph.
// Vite's base option causes transformed imports, CSS, assets and HMR to use it.
const vitePrefix = "/_go_meta_vite/"

type devProxy struct {
	publicPort       string
	backendSocket    string
	socketDir        string
	server           *http.Server
	backendTransport *http.Transport
	mu               sync.Mutex
	connections      map[net.Conn]struct{}
	done             chan error
}

type trackedConn struct {
	net.Conn
	owner *devProxy
}

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.owner.mu.Lock()
	delete(c.owner.connections, c)
	c.owner.mu.Unlock()
	return err
}

type trackedListener struct {
	net.Listener
	owner *devProxy
}

func (l trackedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	wrapped := &trackedConn{Conn: c, owner: l.owner}
	l.owner.mu.Lock()
	l.owner.connections[wrapped] = struct{}{}
	l.owner.mu.Unlock()
	return wrapped, nil
}

func publicDevPort() (string, error) {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("meta dev: invalid PORT %q", port)
	}
	return strconv.Itoa(n), nil
}

func startDevProxy(viteOrigin string, _ io.Writer) (*devProxy, error) {
	port, err := publicDevPort()
	if err != nil {
		return nil, err
	}
	vite, err := url.Parse(viteOrigin)
	if err != nil || vite.Scheme != "http" || vite.Hostname() != "127.0.0.1" || vite.Port() == "" || vite.Path != "" || vite.RawQuery != "" || vite.Fragment != "" || vite.User != nil {
		return nil, fmt.Errorf("meta dev: invalid Vite target")
	}
	if vite.Port() == port {
		return nil, fmt.Errorf("meta dev: PORT and GO_META_VITE_PORT must differ")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		return nil, fmt.Errorf("meta dev: public port %s: %w", port, err)
	}
	dir, err := os.MkdirTemp("", "go-meta-dev-")
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("meta dev: create private backend directory: %w", err)
	}
	socket := filepath.Join(dir, "backend.sock")
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	target := &url.URL{Scheme: "http", Host: "private-backend"}
	p := &devProxy{publicPort: port, backendSocket: socket, socketDir: dir, backendTransport: transport, connections: make(map[net.Conn]struct{}), done: make(chan error, 1)}
	p.server = &http.Server{Handler: devProxyHandlerWithTransport(port, target, vite, transport), ReadHeaderTimeout: 10 * time.Second}
	go func() { p.done <- p.server.Serve(trackedListener{Listener: listener, owner: p}) }()
	return p, nil
}

func localHost(host, port string) bool {
	name, p, err := net.SplitHostPort(host)
	if err != nil {
		name = host
		p = "80"
	}
	return (strings.EqualFold(name, "localhost") || name == "127.0.0.1") && p == port
}

func devProxyHandler(port string, backend, vite *url.URL) http.Handler {
	return devProxyHandlerWithTransport(port, backend, vite, nil)
}

func devProxyHandlerWithTransport(port string, backend, vite *url.URL, backendTransport http.RoundTripper) http.Handler {
	makeProxy := func(target *url.URL, frontend bool) *httputil.ReverseProxy {
		return &httputil.ReverseProxy{
			Rewrite: func(r *httputil.ProxyRequest) {
				r.SetURL(target)
				r.Out.URL.RawQuery = r.In.URL.RawQuery
				if frontend {
					r.Out.Header.Del("Cookie")
					r.Out.Header.Del("Authorization")
					r.Out.Header.Del("Proxy-Authorization")
				} else {
					r.Out.Host = r.In.Host
					r.SetXForwarded()
				}
			},
			ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
				if frontend {
					http.Error(w, "Vite development server unavailable", http.StatusBadGateway)
				} else {
					http.Error(w, "Development backend unavailable; retry after rebuild", http.StatusBadGateway)
				}
			},
		}
	}
	appProxy, viteProxy := makeProxy(backend, false), makeProxy(vite, true)
	appProxy.Transport = backendTransport
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localHost(r.Host, port) {
			http.Error(w, "Invalid development host", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, vitePrefix) {
			viteProxy.ServeHTTP(w, r)
			return
		}
		appProxy.ServeHTTP(w, r)
	})
}

func (p *devProxy) Close() {
	_ = p.server.Close()
	p.mu.Lock()
	conns := make([]net.Conn, 0, len(p.connections))
	for c := range p.connections {
		conns = append(conns, c)
	}
	p.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
	p.backendTransport.CloseIdleConnections()
	_ = os.RemoveAll(p.socketDir)
}
