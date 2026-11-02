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

// validateDynamicAmbiguity enforces the Phase 1 dynamic-route contract:
//
//   - A route may contain at most one dynamic segment: /blog/[year]/[slug]
//     (two parameters) is out of scope for Phase 1.
//   - No two routes may bind different parameter names to the same position
//     (e.g. [id] and [slug] under the same parent), because both normalize
//     to the same URL shape and Fiber could not route between them.
func validateDynamicAmbiguity(routes []Route) error {
	type dynamicKey struct {
		prefix string // static segments before the parameter
		suffix string // static segments after the parameter
	}
	slots := make(map[dynamicKey]string)
	for _, r := range routes {
		segs := strings.Split(strings.TrimPrefix(r.Path, "/"), "/")
		var names []string
		for _, seg := range segs {
			if strings.HasPrefix(seg, ":") {
				names = append(names, seg[1:])
			}
		}
		if len(names) > 1 {
			return fmt.Errorf(
				"routing: %s: route %q has %d dynamic segments (%s): Phase 1 supports at most one per route",
				r.SourceFile, r.Path, len(names), strings.Join(names, ", "),
			)
		}
		for i, seg := range segs {
			if !strings.HasPrefix(seg, ":") {
				continue
			}
			key := dynamicKey{
				prefix: strings.Join(segs[:i], "/"),
				suffix: strings.Join(segs[i+1:], "/"),
			}
			if prevName, ok := slots[key]; ok && prevName != seg[1:] {
				return fmt.Errorf(
					"routing: ambiguous dynamic segments %q and %q: both match the same URL shape under %q: use one parameter name per position",
					prevName, seg[1:], "/"+key.prefix,
				)
			}
			slots[key] = seg[1:]
		}
	}
	return nil
}

// validateLayoutChain enforces the single visual-layout contract and the
// combinations the Go and client-only Svelte renderers can actually serve.
func validateLayoutChain(r Route) error {
	if len(r.Layouts) > 1 {
		return fmt.Errorf("routing: route %q has multiple visual layouts: select only the nearest layout", r.Path)
	}
	if len(r.Layouts) == 1 && r.Layouts[0].Mode == ModeSSR && pathHasParamSegment(r.Layouts[0].SourceFile) {
		return fmt.Errorf("routing: templ layout %q is inside a dynamic segment directory: Go import paths cannot contain brackets; move the layout outside the dynamic segment or use layout.svelte", r.Layouts[0].SourceFile)
	}
	if len(r.Layouts) == 1 && r.Layouts[0].Mode == ModeCSR && r.Mode == ModeSSR {
		return fmt.Errorf(
			"routing: route %q: SSR page %q cannot render beneath CSR layout %q: move the page into a directory without a layout.svelte ancestor, or make it page.svelte",
			r.Path, r.SourceFile, r.Layouts[0].SourceFile,
		)
	}
	return nil
}
