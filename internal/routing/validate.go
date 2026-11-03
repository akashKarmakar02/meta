package routing

import (
	"fmt"
	"path"
	"strings"
)

// ValidateRoutes applies post-discovery collision rules to the discovered
// route set. B08 scope: duplicate normalized routes, which arise when one
// directory holds more than one kind of page file (page.templ and
// page.svelte). C08 adds ambiguous dynamic-parameter collision detection.
func ValidateRoutes(routes []Route) error {
	routes = append([]Route(nil), routes...)
	sortRoutes(routes)
	byPath := map[string]Route{}
	for _, r := range routes {
		if prev, dup := byPath[r.Path]; dup {
			return fmt.Errorf(
				"routing: duplicate route %q from %q and %q: declarations match the same URL shape",
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
	if err := validateDynamicAmbiguity(routes); err != nil {
		return err
	}
	if err := validateCatchAllConflicts(routes); err != nil {
		return err
	}
	return validateEncodedLiteralOverlaps(routes)
}

// validateDynamicAmbiguity rejects repeated names, routes over Fiber's 30
// parameter limit, and equivalent URL shapes. A catch-all segment ("+") is
// one parameter and a distinct shape marker (B03).
func validateDynamicAmbiguity(routes []Route) error {
	shapes := map[string]Route{}
	for _, r := range routes {
		segs := strings.Split(strings.TrimPrefix(r.Path, "/"), "/")
		names := map[string]bool{}
		catchAlls := 0
		for i, seg := range segs {
			switch {
			case seg == "+":
				catchAlls++
				// The declared catch-all name must not collide with a named
				// parameter in the same route (B03).
				if r.CatchAll != "" {
					if names[r.CatchAll] {
						return fmt.Errorf("routing: %s: duplicate parameter name %q in route %q", r.SourceFile, r.CatchAll, r.Path)
					}
					names[r.CatchAll] = true
				}
			case strings.HasPrefix(seg, ":"):
				name := seg[1:]
				if names[name] {
					return fmt.Errorf("routing: %s: duplicate parameter name %q in route %q", r.SourceFile, name, r.Path)
				}
				names[name] = true
				segs[i] = ":"
			}
		}
		// Fiber's route parser supports at most 30 parameters per route; a
		// catch-all counts as one.
		count := len(names)
		if r.CatchAll == "" {
			count += catchAlls
		}
		if count > 30 {
			return fmt.Errorf("routing: %s: route %q has %d parameters; Fiber supports at most 30", r.SourceFile, r.Path, count)
		}
		shape := strings.Join(segs, "/")
		if prev, ok := shapes[shape]; ok {
			return fmt.Errorf("routing: ambiguous dynamic segments in routes %q (%s) and %q (%s): both match the same URL shape", prev.Path, prev.SourceFile, r.Path, r.SourceFile)
		}
		shapes[shape] = r
	}
	return nil
}

// validateCatchAllConflicts rejects catch-all routes that overlap another
// route in a way Fiber cannot resolve deterministically (B03).
//
// A catch-all C with prefix P (the segments before its "+") matches every URL
// under P with one or more extra segments. Another route R whose path extends
// P therefore overlaps C. The overlap is safe only in the two cases the
// framework supports:
//   - R's segments after P are all static.
//   - R's segments after P are exactly one dynamic segment: the documented
//     one-segment overlap. At the first differing segment, static wins over
//     dynamic, which wins over catch-all.
//
// Anything else — a nested catch-all, a multi-segment dynamic route, or a
// mixed static/dynamic route under the prefix — is conservatively rejected
// pending support for a broader overlap policy.
func validateCatchAllConflicts(routes []Route) error {
	for _, c := range routes {
		cSegs := strings.Split(strings.TrimPrefix(c.Path, "/"), "/")
		plusIdx := -1
		for i, s := range cSegs {
			if s == "+" {
				plusIdx = i
				break
			}
		}
		if plusIdx < 0 {
			continue
		}
		prefix := cSegs[:plusIdx]
		for _, r := range routes {
			if r.Path == c.Path {
				continue
			}
			if conflict, ok := catchAllConflict(prefix, strings.Split(strings.TrimPrefix(r.Path, "/"), "/")); ok {
				return fmt.Errorf("routing: ambiguous catch-all conflict between %q (%s) and %q (%s): %s", c.Path, c.SourceFile, r.Path, r.SourceFile, conflict)
			}
		}
	}
	return nil
}

// catchAllConflict reports whether route segments rSegs (with its prefix
// already matched by catch-all prefix prefix) conflict with the catch-all.
// rSegs must be longer than prefix and share its prefix; the caller filters
// that. It returns a human-readable reason and true when the overlap is
// ambiguous.
func catchAllConflict(prefix, rSegs []string) (string, bool) {
	if len(rSegs) <= len(prefix) {
		return "", false
	}
	for i, p := range prefix {
		if !segmentCompatible(p, rSegs[i]) {
			return "", false
		}
	}
	post := rSegs[len(prefix):]
	// A nested catch-all under this catch-all's prefix is ambiguous.
	for _, s := range post {
		if s == "+" {
			return "a catch-all cannot nest under another catch-all", true
		}
	}
	allStatic := true
	dynamicCount := 0
	for _, s := range post {
		if strings.HasPrefix(s, ":") {
			dynamicCount++
			allStatic = false
		}
	}
	switch {
	case allStatic:
		return "", false // specific static path wins; deterministic
	case dynamicCount == 1 && len(post) == 1:
		return "", false // documented one-segment dynamic overlap
	default:
		return "a dynamic route overlaps a catch-all beyond a single segment", true
	}
}

// segmentCompatible reports whether two normalized segments can match the
// same URL segment: equal statics, or at least one dynamic.
func segmentCompatible(a, b string) bool {
	if a == b {
		return true
	}
	return strings.HasPrefix(a, ":") || strings.HasPrefix(b, ":")
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

// Fiber preserves escapes for reserved ASCII in request paths. A dynamic
// route could therefore capture an encoded spelling of a competing literal.
// Reject such overlaps rather than changing escaped-path semantics.
func validateEncodedLiteralOverlaps(routes []Route) error {
	for i, a := range routes {
		as := strings.Split(path.Dir(a.SourceFile), "/")
		for _, b := range routes[i+1:] {
			bs := strings.Split(path.Dir(b.SourceFile), "/")
			unsafe := false
			overlap := true
			for n := 0; n < len(as) && n < len(bs); n++ {
				x, y := as[n], bs[n]
				_, xd := trimParamBrackets(x)
				_, yd := trimParamBrackets(y)
				_, xc := trimCatchAllBrackets(x)
				_, yc := trimCatchAllBrackets(y)
				if (xd || xc) && !yd && !yc && strings.ContainsAny(y, "!$&'()*+,;=:@%# ?\\") || (yd || yc) && !xd && !xc && strings.ContainsAny(x, "!$&'()*+,;=:@%# ?\\") {
					unsafe = true
				}
				if !xd && !xc && !yd && !yc && x != y {
					overlap = false
					break
				}
				if xc || yc {
					rest := bs[n:]
					if yc {
						rest = as[n:]
					}
					for _, segment := range rest {
						if strings.ContainsAny(segment, "!$&'()*+,;=:@%# ?\\") {
							unsafe = true
						}
					}
					break
				}
			}
			if len(as) != len(bs) {
				shorter := as
				if len(bs) < len(as) {
					shorter = bs
				}
				_, catch := trimCatchAllBrackets(shorter[len(shorter)-1])
				if !catch {
					overlap = false
				}
			}
			if overlap && unsafe {
				return fmt.Errorf("routing: ambiguous encoded static segment in routes %q (%s) and %q (%s): Fiber preserves escapes for reserved characters", a.Path, a.SourceFile, b.Path, b.SourceFile)
			}
		}
	}
	return nil
}
