// Package main is the example Fiber v3 executable for the go-meta framework.
package main

import (
	"crypto/rand"
	"log"
	"net"
	"os"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"

	"go-meta/action"
	"go-meta/app/actions"
	"go-meta/app/loaders"
	"go-meta/cache"
	"go-meta/formcsrf"
	"go-meta/loader"

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
	return newConfiguredApp(assets, privateSocket, nil)
}

const loaderRegistryKey = "go-meta/loader-registry"

func newLoaderRegistry(bindings []loader.Binding) (*loader.Registry, error) {
	routes := routes.RegisterRoutes()
	ids := make([]string, len(routes))
	for i, route := range routes {
		ids[i] = route.ID
	}
	return loader.NewRegistry(ids, bindings...)
}

// newActionRegistry builds the immutable action registry from the app's
// bindings. Invalid or duplicate IDs fail here, before any server starts
// publishing a half-configured backend.
func newActionRegistry(bindings []action.Binding) (*action.Registry, error) {
	return action.NewRegistry(bindings...)
}

// newConfiguredAppWithActions adds D02 action dispatch on top of the loader
// app. TrustedOrigin is explicit: production sets GO_META_TRUSTED_ORIGIN;
// development and tests pass the concrete origin they serve on. An empty
// trusted origin fails every action request closed (sameOrigin returns false),
// so a misconfigured deployment never exposes an open mutation endpoint.
func newConfiguredAppWithActions(assets *httpserver.Assets, privateSocket bool, loaders *loader.Registry, actions_ *action.Registry, trustedOrigin string, csrfManager *formcsrf.Manager) *fiber.App {
	fapp := newConfiguredAppBase(assets, privateSocket, loaders, csrfManager, trustedOrigin)
	httpserver.MountActions(fapp, actions_, httpserver.ActionEndpointConfig{TrustedOrigin: trustedOrigin, CSRF: csrfManager})
	return fapp
}

// newConfiguredApp builds a server without action endpoints. Kept for tests
// predating Phase D.
func newConfiguredApp(assets *httpserver.Assets, privateSocket bool, registry *loader.Registry) *fiber.App {
	return newConfiguredAppBase(assets, privateSocket, registry, nil)
}

// newConfiguredAppBase constructs the shared app shell. The CSRF middleware
// must be registered BEFORE page routes: Fiber matches middleware and routes
// in registration order, so a Use after route registration would never see
// page requests.
func newConfiguredAppBase(assets *httpserver.Assets, privateSocket bool, registry *loader.Registry, csrfManager *formcsrf.Manager, trustedOrigin ...string) *fiber.App {
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
	if registry != nil {
		fapp.State().Set(loaderRegistryKey, registry)
	}

	// Recover converts panics into 500 responses instead of killing the server.
	fapp.Use(recover.New())
	// Request logging; default config logs method, path, status, latency —
	// never request or response bodies.
	fapp.Use(logger.New())
	fapp.Use(httpserver.ValidatePagePath)

	// D03: issue a per-browser CSRF cookie + render-context token for pages
	// that render protected forms. Registered BEFORE page routes; no-op
	// without a manager (pre-D03 apps are unaffected).
	if csrfManager != nil {
		fapp.Use(httpserver.WithFormCSRF(csrfManager, trustedOrigin...))
	}

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
			// C03: run the route's registered loader (if any) before rendering,
			// attaching the typed result to the render context. WithLoader is a
			// no-op when the registry is nil or the route has no binding.
			fapp.Get(routing.FiberRegistrationPath(route.Path), httpserver.WithRouteParams(route, httpserver.WithLoader(registry, route, render.SSRNavigationHandler(route, assets, routes.SSRPage, routes.BarePage, routes.VisualLayout))))
		case routing.ModeCSR:
			// C04: run the route's registered loader (if any) before serving the
			// CSR shell or envelope, serializing the result to JSON for the
			// browser. WithLoader is a no-op when the registry is nil or the
			// route has no binding.
			fapp.Get(routing.FiberRegistrationPath(route.Path), httpserver.WithRouteParams(route, httpserver.WithLoader(registry, route, render.CSRNavigationHandler(route, assets, routes.VisualLayout))))
		}
	}

	return fapp
}

func main() {
	demoCache, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 128, MaxBytes: 1 << 20})
	if err != nil {
		log.Fatalf("cache configuration: %v", err)
	}
	defer demoCache.Close()
	// E04: one invalidator coordinates this store; every cached binding over
	// it attaches the same invalidator (enforced at registry construction).
	demoInvalidator, err := loader.NewInvalidator(demoCache)
	if err != nil {
		log.Fatalf("cache invalidator: %v", err)
	}
	actions.DemoInvalidator = demoInvalidator
	bindings := append(loaders.Bindings(), loaders.CachedDemoBindings(demoCache, demoInvalidator)...)
	registry, err := newLoaderRegistry(bindings)
	if err != nil {
		log.Fatalf("loader registration: %v", err)
	}
	actionRegistry, err := newActionRegistry(actions.Bindings())
	if err != nil {
		log.Fatalf("action registration: %v", err)
	}
	var assets httpserver.Assets
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
	csrfManager, err := formcsrf.New(formSecret(), nil)
	if err != nil {
		log.Fatalf("form csrf manager: %v", err)
	}
	app := newConfiguredAppWithActions(&assets, devOrigin != "" && socket != "", registry, actionRegistry, trustedActionOrigin(), csrfManager)
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

// trustedActionOrigin resolves the origin action requests must come from.
// GO_META_TRUSTED_ORIGIN wins (production behind proxies: set it explicitly,
// e.g. "https://example.com" — forwarded headers are never trusted, so an
// explicit value is the only safe configuration). The default covers the
// development and example-server shape: the public origin the server itself
// serves on.
// formSecret returns the form-CSRF signing secret. Production sets
// GO_META_FORM_SECRET; otherwise a random per-process secret is generated.
func formSecret() []byte {
	if s := os.Getenv("GO_META_FORM_SECRET"); s != "" {
		return []byte(s)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		log.Fatal("go-meta: cannot generate form CSRF secret")
	}
	return buf
}

func trustedActionOrigin() string {
	if v := os.Getenv("GO_META_TRUSTED_ORIGIN"); v != "" {
		return v
	}
	if p := os.Getenv("PORT"); p != "" {
		return "http://localhost:" + p
	}
	return "http://localhost:3000"
}
