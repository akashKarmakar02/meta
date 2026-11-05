//go:build imageproc_native

// F03 wiring for the native-tagged example build: construct the native
// processor, resolve the configured source roots, and mount real image
// optimization on the /_meta/image endpoint.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"

	"go-meta/imageopt"
	"go-meta/imageproc"
	"go-meta/internal/httpserver"
	"go-meta/internal/imagecache"
	"go-meta/internal/imageprocessor"
)

// mountImage wires the endpoint when compiled with imageproc_native and
// the runtime libraries are present. The namespace roots are explicit
// environment configuration (GO_META_IMAGE_ROOTS, "namespace=dir,..." —
// default "/images/" anchored at the module's public/images). A missing
// root is a hard startup failure; there is never a silent fallback to an
// arbitrary working directory.
//
// F05 disk cache (GO_META_IMAGE_CACHE_DIR): disabled unless the operator
// names a directory. Limits have explicit defaults; an unparseable limit
// or unusable cache directory is a hard startup failure rather than a
// silently-degraded or unlimited cache.
func mountImage(fapp *fiber.App) {
	limits := imageopt.DefaultLimits()

	mapping := map[string]string{"/images/": defaultImageRoot()}
	if env := os.Getenv("GO_META_IMAGE_ROOTS"); env != "" {
		mapping = parseImageRoots(env)
	}
	resolver, err := httpserver.NewImageResolver(mapping, httpserver.DefaultImageSourceLimits())
	if err != nil {
		fmt.Fprintf(os.Stderr, "image resolver: %v\n", err)
		os.Exit(1)
	}
	imageproc.RegisterBackend(newImageProcessor)
	maxConcurrent := 4
	if v, err := strconv.Atoi(os.Getenv("GO_META_IMAGE_CONCURRENCY")); err == nil && v > 0 && v <= 64 {
		maxConcurrent = v
	}
	backend, perr := imageproc.New(context.Background())
	if perr != nil {
		fmt.Fprintf(os.Stderr, "image backend: %v\n", perr)
		os.Exit(1)
	}
	cfg := httpserver.ImageEndpointConfig{
		Resolver:      resolver,
		Processor:     backend,
		MaxConcurrent: maxConcurrent,
	}
	if dir := os.Getenv("GO_META_IMAGE_CACHE_DIR"); dir != "" {
		cache, cerr := imagecache.Open(imagecache.Config{
			Dir:           dir,
			MaxTotalBytes: envImageBytes("GO_META_IMAGE_CACHE_MAX_TOTAL", 256<<20),
			MaxEntries:    int(envImageBytes("GO_META_IMAGE_CACHE_MAX_ENTRIES", 4096)),
			MaxEntryBytes: envImageBytes("GO_META_IMAGE_CACHE_MAX_ENTRY", 64<<20),
		})
		if cerr != nil {
			fmt.Fprintf(os.Stderr, "image cache: %v\n", cerr)
			os.Exit(1)
		}
		cfg.Cache = cache
	}
	if err := httpserver.MountImageProcessing(fapp, limits, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "image endpoint: %v\n", err)
		os.Exit(1)
	}
}

// envImageBytes reads a positive byte count from env; def is used when
// unset or invalid. Values are clamped to a sane ceiling (1 GiB) so a typo
// cannot configure an unbounded cache.
func envImageBytes(name string, def int64) int64 {
	v, err := strconv.ParseInt(os.Getenv(name), 10, 64)
	if err != nil || v < 1 {
		return def
	}
	if v > 1<<30 {
		return 1 << 30
	}
	return v
}

// parseImageRoots parses "namespace=dir" comma pairs. Duplicate
// namespaces and empty values fail closed at startup.
func parseImageRoots(env string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(env, ",") {
		ns, dir, ok := strings.Cut(pair, "=")
		if !ok || ns == "" || dir == "" {
			fmt.Fprintf(os.Stderr, "GO_META_IMAGE_ROOTS: bad entry %q (want namespace=dir)\n", pair)
			os.Exit(1)
		}
		if _, dup := out[ns]; dup {
			fmt.Fprintf(os.Stderr, "GO_META_IMAGE_ROOTS: duplicate namespace %q\n", ns)
			os.Exit(1)
		}
		out[ns] = dir
	}
	return out
}

// defaultImageRoot anchors the /images/ namespace at the module's
// public/images directory: resolved from this file's location at build
// time via the module root, so any working directory gets the same root.
func defaultImageRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "public", "images")
}

// defaultImageProcessingAvailable reports whether the native backend
// constructs and can actually encode WebP on this host.
func defaultImageProcessingAvailable() bool {
	n, err := imageprocessor.New(context.Background())
	if err != nil {
		return false
	}
	return n.Capabilities().WebPEncode
}

// newImageProcessor is the single registered backend constructor. One
// named function (not a per-call closure) so repeated wiring in tests is
// the same function value and RegisterBackend stays idempotent.
func newImageProcessor(ctx context.Context) (imageproc.Processor, error) {
	return imageprocessor.New(ctx)
}
