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

	"go-meta/internal/httpserver"
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
	// Outage notice state, guarded by mu: the first failure after a period
	// of health prints one line; recovery prints one more. Per-request noise
	// would bury Air's build output.
	outageAnnounced bool
	log             io.Writer
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

func startDevProxy(viteOrigin string, log io.Writer) (*devProxy, error) {
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
		return nil, formatPublicPortError("127.0.0.1:"+port, err)
	}
	dir, err := httpserver.NewDevSessionDir()
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("meta dev: create private backend directory: %w", err)
	}
	socket := filepath.Join(dir, "backend.sock")
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		d := net.Dialer{Timeout: 3 * time.Second} // fail fast while the backend restarts
		return d.DialContext(ctx, "unix", socket)
	}}
	target := &url.URL{Scheme: "http", Host: "private-backend"}
	p := &devProxy{publicPort: port, backendSocket: socket, socketDir: dir, backendTransport: transport, connections: make(map[net.Conn]struct{}), done: make(chan error, 1), log: log}
	p.server = &http.Server{Handler: devProxyHandlerWithTransport(port, target, vite, transport, p), ReadHeaderTimeout: 10 * time.Second}
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
	return devProxyHandlerWithTransport(port, backend, vite, nil, nil)
}

// noteBackendOutage records a failed backend request and reports whether
// this outage transition deserves its single notice line.
func (p *devProxy) noteBackendOutage() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.outageAnnounced {
		return false
	}
	p.outageAnnounced = true
	return true
}

// noteBackendRecovery clears the outage notice so the next outage prints
// again and prints the recovery line once.
func (p *devProxy) noteBackendRecovery() {
	if p == nil {
		return
	}
	p.mu.Lock()
	was := p.outageAnnounced
	p.outageAnnounced = false
	p.mu.Unlock()
	if was && p.log != nil {
		fmt.Fprintln(p.log, "[meta] backend recovered; requests are being served again")
	}
}

// rootCause condenses a transport error for display. Unix dial failures
// read like `dial unix /path/socket: connect: connection refused`; the
// useful part for a developer is the tail.
func rootCause(err error) string {
	if err == nil {
		return "unknown error"
	}
	msg := err.Error()
	const marker = "connect: "
	if i := strings.LastIndex(msg, marker); i >= 0 {
		return msg[i+len(marker):]
	}
	return msg
}

// navigationEnvelopeRequest reports whether the request asks for the
// soft-navigation JSON envelope. Those requests must never see HTML or a
// 200 error body (A06 §7): plain text plus 503 keeps failure legible.
func navigationEnvelopeRequest(r *http.Request) bool {
	return r.Header.Get("X-Go-Web-Navigation") != ""
}

func devProxyHandlerWithTransport(port string, backend, vite *url.URL, backendTransport http.RoundTripper, proxy *devProxy) http.Handler {
	makeProxy := func(target *url.URL, frontend bool) *httputil.ReverseProxy {
		rp := &httputil.ReverseProxy{
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
			ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
				if frontend {
					http.Error(w, "Vite development server unavailable ("+rootCause(err)+")", http.StatusBadGateway)
					return
				}
				if proxy.noteBackendOutage() && proxy.log != nil {
					fmt.Fprintf(proxy.log, "[meta] backend temporarily unavailable while Air rebuilds or restarts (%s); the public server stays up and will recover automatically\n", rootCause(err))
				}
				writeBackendUnavailable(w, req, err)
			},
		}
		if frontend {
			// A hung Vite upstream must not wedge the proxy: bound waits let
			// clients retry instead of blocking forever (A06 §7).
			rp.Transport = &http.Transport{
				ResponseHeaderTimeout: 30 * time.Second,
				DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			}
		}
		return rp
	}
	appProxy, viteProxy := makeProxy(backend, false), makeProxy(vite, true)
	appProxy.Transport = backendTransport
	appProxy.ModifyResponse = func(*http.Response) error {
		proxy.noteBackendRecovery()
		return nil
	}
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

// writeBackendUnavailable answers a request whose backend is down with a
// 503 that recovers automatically once Air finishes. Browsers get a small
// HTML page; navigation-envelope requests get plain text so nothing can be
// mistaken for a valid envelope.
func writeBackendUnavailable(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Retry-After", "1")
	w.Header().Set("Cache-Control", "no-store")
	if navigationEnvelopeRequest(r) || !acceptsHTML(r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, "go-meta development backend unavailable (%s); retrying automatically after the current rebuild\n", rootCause(err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprintf(w, `<!doctype html>
<html><head><meta charset="utf-8"><title>go-meta development backend restarting</title></head>
<body style="font-family: system-ui, sans-serif; margin: 3rem auto; max-width: 36rem;">
<h1>Development backend restarting</h1>
<p>The Go backend is rebuilding or restarting (Air hot reload). The public
server stays up; this page will work again as soon as the new build finishes.</p>
<p>Refresh in a moment, or <a href="/">go to the app root</a>. Details: <code>%s</code></p>
</body></html>`, rootCause(err))
}

// acceptsHTML reports whether the client prefers an HTML response.
func acceptsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
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
