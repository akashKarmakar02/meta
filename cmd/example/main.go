// Package main is the example Fiber v3 executable for the go-meta framework.
package main

import (
	"log"
	"net"
	"os"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"

	routes "go-meta/generated"
	"go-meta/internal/httpserver"
	"go-meta/internal/render"
	"go-meta/internal/routing"
)

// newApp constructs the Fiber application with its middleware and routes.
// assets carries the Vite-manifest-resolved client entry URLs; nil renders
// bare CSR shells without scripts (test/fixture mode). Production main
// loads the manifest and fails explicitly on missing/invalid manifests
// rather than serving shells that cannot boot (E08 contract).
func newApp(assets *httpserver.Assets) *fiber.App {
	return newAppWithDevProxy(assets, false)
}

func newAppWithDevProxy(assets *httpserver.Assets, privateSocket bool) *fiber.App {
	config := fiber.Config{
		// D07: centralized HTML error pages; no stack traces or internals leak.
		ErrorHandler:  render.HTTPErrorHandler,
		CaseSensitive: true,
	}
	if privateSocket {
		// The private socket is accessible only to this session's proxy,
		// which discards incoming forwarded headers and sets this itself.
		config.ProxyHeader = "X-Forwarded-For"
		config.TrustProxy = true
		config.TrustProxyConfig.UnixSocket = true
	}
	fapp := fiber.New(config)

	// Recover converts panics into 500 responses instead of killing the server.
	fapp.Use(recover.New())
	// Request logging; default config logs method, path, status, latency —
	// never request or response bodies.
	fapp.Use(logger.New())
	fapp.Use(httpserver.ValidatePagePath)

	fapp.Get("/healthz", func(c fiber.Ctx) error {
		return c.SendString("ok")
	})

	// Generated page routes: registered from build-time codegen as plain
	// compiled data — no request-time discovery, no reflection. Order is
	// the manifest (path-sorted) order; paths are unique post-validation
	// (B08), so no route can be registered twice.
	for _, r := range routes.RegisterRoutes() {
		route := r
		switch route.Mode {
		case routing.ModeSSR:
			fapp.Get(routing.FiberRegistrationPath(route.Path), httpserver.WithRouteParams(route, render.SSRNavigationHandler(route, assets, routes.SSRPage, routes.BarePage, routes.VisualLayout)))
		case routing.ModeCSR:
			fapp.Get(routing.FiberRegistrationPath(route.Path), httpserver.WithRouteParams(route, render.CSRNavigationHandler(route, assets, routes.VisualLayout)))
		}
	}

	return fapp
}

func main() {
	var assets httpserver.Assets
	var err error
	devOrigin := os.Getenv("GO_META_VITE_ORIGIN")
	if devOrigin != "" {
		assets, err = httpserver.DevelopmentAssets(devOrigin)
	} else {
		assets, err = httpserver.LoadAssets("dist/client")
	}
	if err != nil {
		log.Fatalf("load client assets: %v", err)
	}
	socket := os.Getenv("GO_META_DEV_SOCKET")
	app := newAppWithDevProxy(&assets, devOrigin != "" && socket != "")
	// E09: serve the hashed production bundle at the same /assets prefix
	// the shells reference. Fails explicitly when the build output is
	// missing rather than serving pages that cannot boot.
	if devOrigin == "" {
		if err := httpserver.MountAssets(app, "dist/client"); err != nil {
			log.Fatalf("mount client assets: %v", err)
		}
	}
	if devOrigin != "" && socket != "" {
		// The CLI owns this private directory; Air may leave a stale socket
		// after stopping the preceding backend during a rebuild.
		// RemoveStaleSocket only ever removes an actual Unix socket at this
		// exact path (never a regular file or a symlink target).
		if err := httpserver.RemoveStaleSocket(socket); err != nil {
			log.Fatalf("remove stale development socket: %v", err)
		}
		listener, err := net.Listen("unix", socket)
		if err != nil {
			log.Fatalf("listen on development socket: %v", err)
		}
		log.Fatal(app.Listener(listener))
	}
	// PORT overrides the default :3000 (E2E runs the same binary on a
	// scratch port; production picks its own through the environment).
	addr := ":3000"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}
	log.Fatal(app.Listen(addr))
}
