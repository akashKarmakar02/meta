package routing

import (
	"fmt"
	"strings"
)

// ValidateRoutes applies post-discovery collision rules to the discovered
// route set. B08 scope: duplicate normalized routes, which arise when one
// directory holds more than one kind of page file (page.templ and
// page.svelte). C08 adds ambiguous dynamic-parameter collision detection.
func ValidateRoutes(routes []Route) error {
	byPath := map[string]Route{}
	for _, r := range routes {
		if prev, dup := byPath[r.Path]; dup {
			return fmt.Errorf(
				"routing: duplicate route %q from %q and %q: choose one page file per directory (page.templ or page.svelte), not both",
				r.Path, prev.SourceFile, r.SourceFile,
			)
		}
		byPath[r.Path] = r
	}
	for _, r := range routes {
		if err := validateLayoutChain(r); err != nil {
			return err
		}
	}
	return validateDynamicAmbiguity(routes)
}

// validateDynamicAmbiguity rejects repeated names and equivalent URL shapes.
func validateDynamicAmbiguity(routes []Route) error {
	shapes := map[string]Route{}
	for _, r := range routes {
		segs := strings.Split(strings.TrimPrefix(r.Path, "/"), "/")
		names := map[string]bool{}
		for i, seg := range segs {
			if strings.HasPrefix(seg, ":") {
				name := seg[1:]
				if names[name] {
					return fmt.Errorf("routing: %s: duplicate parameter name %q in route %q", r.SourceFile, name, r.Path)
				}
				names[name] = true
				segs[i] = ":"
			}
		}
		// Fiber's route parser supports at most 30 parameters per route.
		if len(names) > 30 {
			return fmt.Errorf("routing: %s: route %q has %d parameters; Fiber supports at most 30", r.SourceFile, r.Path, len(names))
		}
		shape := strings.Join(segs, "/")
		if prev, ok := shapes[shape]; ok {
			return fmt.Errorf("routing: ambiguous dynamic segments in routes %q (%s) and %q (%s): both match the same URL shape", prev.Path, prev.SourceFile, r.Path, r.SourceFile)
		}
		shapes[shape] = r
	}
	return nil
}

// validateLayoutChain enforces the single visual-layout contract and the
// combinations the Go and client-only Svelte renderers can actually serve.
func validateLayoutChain(r Route) error {
	if len(r.Layouts) > 1 {
		return fmt.Errorf("routing: route %q has multiple visual layouts: select only the nearest layout", r.Path)
	}
	if len(r.Layouts) == 1 && r.Layouts[0].Mode == ModeCSR && r.Mode == ModeSSR {
		return fmt.Errorf(
			"routing: route %q: SSR page %q cannot render beneath CSR layout %q: move the page into a directory without a layout.svelte ancestor, or make it page.svelte",
			r.Path, r.SourceFile, r.Layouts[0].SourceFile,
		)
	}
	return nil
}
