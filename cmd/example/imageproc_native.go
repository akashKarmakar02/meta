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

	"github.com/akashKarmakar02/meta/imageopt"
	"github.com/akashKarmakar02/meta/imageproc"
	"github.com/akashKarmakar02/meta/internal/httpserver"
	"github.com/akashKarmakar02/meta/internal/imagecache"
	"github.com/akashKarmakar02/meta/internal/imageprocessor"
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
	concurrency, err := envImageLimit("GO_META_IMAGE_CONCURRENCY", 4, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "image endpoint: %v\n", err)
		os.Exit(1)
	}
	backend, perr := imageproc.New(context.Background())
	if perr != nil {
		fmt.Fprintf(os.Stderr, "image backend: %v\n", perr)
		os.Exit(1)
	}
	cfg := httpserver.ImageEndpointConfig{
		Resolver:      resolver,
		Processor:     backend,
		MaxConcurrent: int(concurrency),
	}
	if dir := os.Getenv("GO_META_IMAGE_CACHE_DIR"); dir != "" {
		if err := imageCachePath(dir, mapping); err != nil {
			fmt.Fprintf(os.Stderr, "image cache: %v\n", err)
			os.Exit(1)
		}
		total, terr := envImageLimit("GO_META_IMAGE_CACHE_MAX_TOTAL", 256<<20, 1<<30)
		entries, eerr := envImageLimit("GO_META_IMAGE_CACHE_MAX_ENTRIES", 4096, 1<<30)
		entry, xerr := envImageLimit("GO_META_IMAGE_CACHE_MAX_ENTRY", 64<<20, 1<<30)
		if terr != nil || eerr != nil || xerr != nil {
			fmt.Fprintf(os.Stderr, "image cache: invalid configured limit\n")
			os.Exit(1)
		}
		cache, cerr := imagecache.Open(imagecache.Config{
			Dir:           dir,
			MaxTotalBytes: total,
			MaxEntries:    int(entries),
			MaxEntryBytes: entry,
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

// envImageLimit uses defaults only for unset settings and rejects invalid
// explicit values rather than silently running with a different limit.
func envImageLimit(name string, def, max int64) (int64, error) {
	raw, ok := os.LookupEnv(name)
	if !ok {
		return def, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 1 || v > max {
		return 0, fmt.Errorf("%s must be a positive integer at most %d", name, max)
	}
	return v, nil
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
