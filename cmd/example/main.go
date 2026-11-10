// Package main is the example Fiber v3 executable for the go-meta framework.
package main

import (
	"crypto/rand"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/recover"

	"github.com/akashKarmakar02/meta/action"
	"github.com/akashKarmakar02/meta/app/actions"
	"github.com/akashKarmakar02/meta/app/loaders"
	"github.com/akashKarmakar02/meta/cache"
	"github.com/akashKarmakar02/meta/env"
	"github.com/akashKarmakar02/meta/formcsrf"
	"github.com/akashKarmakar02/meta/loader"
	"github.com/akashKarmakar02/meta/metadata"
	"github.com/akashKarmakar02/meta/observability"
	"github.com/akashKarmakar02/meta/server"
	"github.com/akashKarmakar02/meta/sitemap"

	routes "github.com/akashKarmakar02/meta/generated"
	"github.com/akashKarmakar02/meta/internal/httpserver"
	"github.com/akashKarmakar02/meta/internal/render"
	"github.com/akashKarmakar02/meta/internal/routing"
	pagecache "github.com/akashKarmakar02/meta/pagecache"
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
func newConfiguredAppWithActions(assets *httpserver.Assets, privateSocket bool, loaders *loader.Registry, actions_ *action.Registry, trustedOrigin string, csrfManager *formcsrf.Manager, preRoute ...fiber.Handler) *fiber.App {
	fapp := newConfiguredAppBase(assets, privateSocket, loaders, csrfManager, []string{trustedOrigin}, preRoute...)
	httpserver.MountActions(fapp, actions_, httpserver.ActionEndpointConfig{TrustedOrigin: trustedOrigin, CSRF: csrfManager})
	return fapp
}

// newConfiguredApp builds a server without action endpoints. Kept for tests
// predating Phase D.
func newConfiguredApp(assets *httpserver.Assets, privateSocket bool, registry *loader.Registry, preRoute ...fiber.Handler) *fiber.App {
	return newConfiguredAppBase(assets, privateSocket, registry, nil, nil, preRoute...)
}

// newConfiguredAppBase constructs the shared app shell. The CSRF middleware
// must be registered BEFORE page routes: Fiber matches middleware and routes
// in registration order, so a Use after route registration would never see
// page requests. preRoute (E06) lets tests install request-scoped principal
// middleware the same way real auth middleware would: before any route.
func newConfiguredAppBase(assets *httpserver.Assets, privateSocket bool, registry *loader.Registry, csrfManager *formcsrf.Manager, trustedOrigin []string, preRoute ...fiber.Handler) *fiber.App {
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

	for _, h := range preRoute {
		fapp.Use(h)
	}

	// Recover converts panics into 500 responses instead of killing the server.
	fapp.Use(recover.New())
	fapp.Use(httpserver.ValidatePagePath)

	// Serve public/ directly in both development and production.
	if err := httpserver.MountPublic(fapp, "public"); err != nil {
		log.Printf("public files: %v", err)
	}

	// D03: issue a per-browser CSRF cookie + render-context token for pages
	// that render protected forms. Registered BEFORE page routes; no-op
	// without a manager (pre-D03 apps are unaffected).
	if csrfManager != nil {
		fapp.Use(httpserver.WithFormCSRF(csrfManager, trustedOrigin...))
	}

	// F01/F03: mount the image endpoint before any file-based page route.
	// Native-tagged builds wire real processing (cmd/example/imageproc_native.go);
	// untagged builds keep the truthful reserved 501 endpoint. mountImage
	// itself lives in the build-tag-specific files so exactly one handler
	// is registered for /_meta/image.
	mountImage(fapp)

	fapp.Get("/healthz", func(c fiber.Ctx) error {
		return c.SendString("ok")
	})

	// Generated page routes: registered from build-time codegen as plain
	// compiled data — no request-time discovery, no reflection. Order is
	// the manifest (path-sorted) order; paths are unique post-validation
	// (B08), so no route can be registered twice.
	//
	// PageCache (opt-in via GO_META_PAGE_CACHE): when set, SSR renders are
	// cached and served on hit without re-running loaders or templ. The
	// wrapper sits INSIDE WithRouteParams so the cache key can include the
	// matched pattern and typed params.
	//
	// Production convention (also used by meta init scaffolds): disk-backed
	// with cache.Never — entries live until explicitly invalidated (see
	// PageCache.InvalidatePath) rather than expiring on a timer. Dev always
	// leaves this unset so edits show up immediately.
	var pageCache *pagecache.PageCache
	switch mode := os.Getenv("GO_META_PAGE_CACHE"); mode {
	case "":
		// Caching disabled — every request renders fresh. The dev default.
	case "memory":
		memoryStore, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 256, MaxBytes: 8 << 20})
		if err != nil {
			log.Fatalf("page cache configuration: %v", err)
		}
		pageCache = pagecache.NewPageCache(memoryStore, 30*time.Second)
	case "disk":
		diskStore, err := cache.NewDiskStore(cache.DiskConfig{Dir: ".meta/page-cache", MaxEntryBytes: 2 << 20})
		if err != nil {
			log.Fatalf("page cache store: %v", err)
		}
		pageCache = pagecache.NewPageCache(diskStore, time.Hour)
	case "prod":
		// The scaffold default for production: disk store + Never TTL; every
		// entry survives until InvalidatePath is called for it.
		diskStore, err := cache.NewDiskStore(cache.DiskConfig{Dir: ".meta/page-cache", MaxEntryBytes: 2 << 20})
		if err != nil {
			log.Fatalf("page cache store: %v", err)
		}
		pageCache = pagecache.NewPageCache(diskStore, cache.Never)
	default:
		log.Fatalf("page cache: unknown GO_META_PAGE_CACHE value %q (want \"memory\", \"disk\" or \"prod\")", mode)
	}

	// Dynamic sitemap (M4): GO_META_SITEMAP_URL enables /sitemap.xml, built
	// from the discovered page routes and persisted with the disk store when
	// GO_META_SITEMAP_CACHE names a directory. Static-path routes appear by
	// default; parameterized routes are listed when a dynamic URL resolver is
	// registered in the route map below.
	var siteMap *sitemap.Sitemap
	if baseURL := os.Getenv("GO_META_SITEMAP_URL"); baseURL != "" {
		baseURL = strings.TrimSuffix(baseURL, "/")
		smRoutes := make(sitemap.Source, 0, len(routes.RegisterRoutes()))
		for _, r := range routes.RegisterRoutes() {
			smRoutes = append(smRoutes, sitemap.RouteInfo{Path: r.Path})
		}
		smCfg := sitemap.Config{BaseURL: baseURL, RouteSource: smRoutes, Cache: metadata.SitemapCache{TTL: metadata.DefaultSitemapCacheTTL}}
		if dir := os.Getenv("GO_META_SITEMAP_CACHE"); dir != "" {
			diskStore, derr := cache.NewDiskStore(cache.DiskConfig{Dir: dir, MaxEntryBytes: 1 << 20})
			if derr != nil {
				log.Fatalf("sitemap cache store: %v", derr)
			}
			smCfg.Store = diskStore
		}
		var serr error
		siteMap, serr = sitemap.New(smCfg)
		if serr != nil {
			log.Fatalf("sitemap configuration: %v", serr)
		}
	}

	// Serve the sitemap when enabled. Invalidations from mutations flow
	// through siteMap.Invalidate (called by demo actions below).
	if siteMap != nil {
		sm := siteMap
		fapp.Get("/sitemap.xml", func(c fiber.Ctx) error {
			xml, err := sm.XML(c.Context())
			if err != nil {
				return err
			}
			c.Set(fiber.HeaderContentType, "application/xml; charset=utf-8")
			return c.Send(xml)
		})
	}

	ordered := routes.RegisterRoutes()
	apiByID := make(map[string]server.APIRoute)
	for _, api := range routes.RegisterAPIRoutes() {
		apiByID[api.SourceFile] = api
		ordered = append(ordered, routing.Route{ID: api.SourceFile, Path: api.Path, SourceFile: api.SourceFile, Mode: routing.ModeSSR, CatchAll: api.CatchAll})
	}
	routing.SortRoutes(ordered)
	for _, r := range ordered {
		route := r
		if api, ok := apiByID[route.ID]; ok {
			server.MountAPI(fapp, api)
			continue
		}
		switch route.Mode {
		case routing.ModeSSR:
			// C03: run the route's registered loader (if any) before rendering,
			// attaching the typed result to the render context. WithLoader is a
			// no-op when the registry is nil or the route has no binding.
			ssrHandler := httpserver.WithLoader(registry, route, render.SSRNavigationHandler(route, assets, routes.SSRPage, routes.BarePage, routes.VisualLayout))
			if pageCache != nil {
				ssrHandler = pageCache.Wrap(ssrHandler)
			}
			fapp.Get(routing.FiberRegistrationPath(route.Path), httpserver.WithRouteParams(route, ssrHandler))
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
	if os.Getenv("GO_META_DEV") != "1" {
		if err := env.LoadProduction("."); err != nil {
			log.Fatal(err)
		}
	}
	demoCache, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 128, MaxBytes: 1 << 20})
	if err != nil {
		log.Fatalf("cache configuration: %v", err)
	}
	defer demoCache.Close()
	demoStore := observability.WrapCache(demoCache)
	// E04: one invalidator coordinates this store; every cached binding over
	// it attaches the same invalidator (enforced at registry construction).
	demoInvalidator, err := loader.NewInvalidator(demoStore)
	if err != nil {
		log.Fatalf("cache invalidator: %v", err)
	}
	actions.DemoInvalidator = demoInvalidator
	demoFlights, err := loader.NewFlightGroup(demoInvalidator, 5*time.Second)
	if err != nil {
		log.Fatalf("cache flights: %v", err)
	}
	bindings := append(loaders.Bindings(), loaders.CachedDemoBindingsWithFlights(demoStore, demoInvalidator, demoFlights)...)
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
		assets, err = httpserver.DevelopmentAssets(devOrigin, true)
	} else {
		assets, err = httpserver.LoadAssets("dist/client", true)
	}
	if err != nil {
		log.Fatalf("load client assets: %v", err)
	}
	socket := os.Getenv("GO_META_DEV_SOCKET")
	csrfManager, err := formcsrf.New(formSecret(), nil)
	if err != nil {
		log.Fatalf("form csrf manager: %v", err)
	}
	var metrics *observability.Collector
	telemetry := observability.Config{}
	if os.Getenv("GO_META_OBSERVABILITY") != "0" {
		metrics = observability.NewCollector()
		telemetry.Metrics = metrics
	}
	middleware := []fiber.Handler{observability.Middleware(telemetry)}
	// Opt-in cross-origin resource sharing for the example API. Meta never
	// enables CORS by default; GO_META_CORS_ORIGINS exists so the example
	// binary can demonstrate real browser-enforced CORS (and so E2E can
	// verify it) without any change to the framework. Empty = disabled.
	if os.Getenv("GO_META_CORS_ORIGINS") != "" {
		corsHandler, err := exampleCORSMiddleware()
		if err != nil {
			log.Fatalf("cors: %v", err)
		}
		middleware = append(middleware, corsHandler)
	}
	// The example diagnostics endpoint is intended for the loopback-only
	// development proxy. Do not expose it on the production listener.
	if metrics != nil && os.Getenv("GO_META_DEV") == "1" && devOrigin != "" && socket != "" {
		middleware = append(middleware, func(c fiber.Ctx) error {
			if c.Method() != fiber.MethodGet || c.Path() != "/_meta/diagnostics" {
				return c.Next()
			}
			observability.SetRoute(c.Context(), "diagnostics")
			c.Set(fiber.HeaderCacheControl, "no-store")
			return c.JSON(metrics.Snapshot())
		})
	}
	app := newConfiguredAppWithActions(&assets, devOrigin != "" && socket != "", registry, actionRegistry, trustedActionOrigin(), csrfManager, middleware...)
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

// exampleCORSMiddleware builds Fiber's CORS middleware from
// GO_META_CORS_ORIGINS (comma-separated explicit browser origins). Values are
// validated strictly and fail closed: any malformed origin aborts startup
// rather than being skipped. GO_META_CORS_CREDENTIALS=1 opts into credentialed
// requests; wildcard origins are rejected outright because they must never be
// combined with credentials.
func exampleCORSMiddleware() (fiber.Handler, error) {
	origins := make([]string, 0, 4)
	for _, raw := range strings.Split(os.Getenv("GO_META_CORS_ORIGINS"), ",") {
		origin := strings.TrimSpace(raw)
		if origin == "" {
			continue
		}
		if origin == "*" {
			return nil, fmt.Errorf("wildcard origins are not supported by the example configuration")
		}
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(u.Host, " \t\r\n") {
			return nil, fmt.Errorf("invalid CORS origin %q: use scheme://host[:port] with no path", origin)
		}
		origins = append(origins, origin)
	}
	if len(origins) == 0 {
		return nil, fmt.Errorf("no valid origins configured")
	}
	cfg := cors.Config{
		AllowOrigins: origins,
		AllowMethods: []string{fiber.MethodGet, fiber.MethodPost, fiber.MethodPut, fiber.MethodPatch, fiber.MethodDelete, fiber.MethodOptions},
		AllowHeaders: []string{fiber.HeaderOrigin, fiber.HeaderContentType, fiber.HeaderAuthorization},
		// Exposing the approval headers lets E2E (and curious developers)
		// assert the exact-origin echo from inside the browser; a cross-origin
		// script cannot read any header the server does not expose here.
		ExposeHeaders: []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials"},
		MaxAge:        600,
	}
	if os.Getenv("GO_META_CORS_CREDENTIALS") == "1" {
		cfg.AllowCredentials = true
	}
	return cors.New(cfg), nil
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
