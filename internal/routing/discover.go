package routing

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Discover walks an fs.FS rooted at the app directory and describes every
// page route it finds. B07 scope: page.templ/page.svelte classification
// plus layout.templ (SSR) and layout.svelte (CSR) discovery. Each page
// selects the nearest layout; same-directory conflicts are rejected.
//
// Route output is deterministic: sorted by normalized path.
//
// The scanner describes files as data; it never compiles or executes them.
func Discover(fsys fs.FS) ([]Route, error) {
	var routes []Route

	// layouts maps a directory (no trailing slash; "." is the app root) to
	// the layout file found in it.
	layouts := map[string]Layout{}

	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// B01 ignore rules: directories prefixed with underscore hold
			// colocated components, not routes; dot-directories are tooling
			// state (Vite cache, git metadata). Neither is walked.
			base := path.Base(p)
			if strings.HasPrefix(base, "_") || (strings.HasPrefix(base, ".") && p != ".") {
				return fs.SkipDir
			}
			// B01 segment validation: reject malformed bracket declarations
			// during the walk so they never reach generated output.
			return validateSegment(base, p)
		}
		dir, base := path.Split(p)

		// page.static.templ is reserved for future static generation:
		// reject it loudly instead of silently registering or ignoring it.
		if base == "page.static.templ" {
			return fmt.Errorf("routing: STATIC_NOT_IMPLEMENTED: %q: static generation is reserved for a future implementation; rename the file to page.templ (SSR) or page.svelte (CSR)", p)
		}

		if mode, ok := classifyLayout(base); ok {
			// Prefix with the app root so layout IDs carry the
			// repo-rooted form the navigation protocol documents
			// (blueprint 5.1: "app/layout.templ").
			layout, lerr := NewLayout("app/"+p, p, mode)
			if lerr != nil {
				return lerr
			}
			key := strings.TrimSuffix(dir, "/")
			if key == "" {
				key = "."
			}
			if existing, dup := layouts[key]; dup {
				return fmt.Errorf("routing: conflicting layouts in directory %q: %s and %s; do not place layout.templ and layout.svelte in the same folder", key, existing.SourceFile, p)
			}
			layouts[key] = layout
			return nil
		}

		mode, ok := classifyPage(base)
		if !ok {
			return nil
		}
		if mode == ModeSSR && pathHasParamSegment(path.Dir(p)) {
			return fmt.Errorf("routing: SSR page %q is inside a dynamic segment directory: Go import paths cannot contain %q; use page.svelte (CSR) for dynamic routes in Phase 1", p, path.Dir(p))
		}
		route, rerr := NewRoute(normalizeDirToPath(dir), p, mode, nil)
		if rerr != nil {
			return rerr
		}
		routes = append(routes, route)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("routing: walk app tree: %w", err)
	}

	if len(routes) == 0 {
		return nil, fmt.Errorf("routing: no routes found: app tree contains no page.templ or page.svelte")
	}

	for i := range routes {
		routes[i].Layouts = layoutChain(path.Dir(routes[i].SourceFile), layouts)
	}

	sortRoutes(routes)

	if err := ValidateRoutes(routes); err != nil {
		return nil, err
	}
	return routes, nil
}

// layoutChain returns the nearest visual layout, if any.
func layoutChain(routeDir string, layouts map[string]Layout) []Layout {
	for dir := routeDir; ; dir = path.Dir(dir) {
		if l, ok := layouts[dir]; ok {
			return []Layout{l}
		}
		if dir == "." {
			break
		}
	}
	return nil
}

// classifyPage maps a filename to its render mode. Only exact page
// filenames count (binding decision #4); anything else — including layout
// and other component files — is not a page.
func classifyPage(base string) (RenderMode, bool) {
	switch base {
	case "page.templ":
		return ModeSSR, true
	case "page.svelte":
		return ModeCSR, true
	default:
		return "", false
	}
}

// classifyLayout maps a layout filename to its render mode: layout.templ is
// an SSR layout, layout.svelte a CSR layout. Both exact filenames count;
// anything else is a component.
func classifyLayout(base string) (RenderMode, bool) {
	switch base {
	case "layout.templ":
		return ModeSSR, true
	case "layout.svelte":
		return ModeCSR, true
	default:
		return "", false
	}
}

// normalizeDirToPath converts a slash directory path like "about/" into a
// URL path like "/about". The root directory "" maps to "/". Bracketed
// segments translate to Fiber parameter syntax: "products/[id]" becomes
// "/products/:id".
func normalizeDirToPath(dir string) string {
	trimmed := strings.TrimSuffix(dir, "/")
	if trimmed == "" || trimmed == "." {
		return "/"
	}
	segments := strings.Split(trimmed, "/")
	var b strings.Builder
	b.WriteString("/")
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		if b.Len() > 1 {
			b.WriteString("/")
		}
		if param, ok := trimParamBrackets(seg); ok {
			b.WriteString(":")
			b.WriteString(param)
			continue
		}
		b.WriteString(seg)
	}
	return b.String()
}

// trimParamBrackets reports whether seg is a dynamic parameter segment of the
// form "[name]" and returns the bare parameter name.
//
// B01 hardening: any segment that merely looks like bracket syntax is now an
// error instead of silently degrading to a static segment. Malformed forms
// include unbalanced brackets ("[id", "id]"), empty names ("[]"), nested
// brackets ("[[id]"), embedded slashes ("[a/b]"), and catch-all markers
// ("[...slug]", rejected until B03 implements them).
func trimParamBrackets(seg string) (string, bool) {
	if !strings.HasPrefix(seg, "[") || !strings.HasSuffix(seg, "]") {
		return "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(seg, "["), "]")
	if name == "" || strings.ContainsAny(name, "[]/") {
		return "", false
	}
	return name, true
}

// validateSegment rejects directory names that declare bracket intent but do
// not form a valid Phase 1 dynamic parameter segment. It runs before
// normalizeDirToPath so malformed segments never reach generated output.
func validateSegment(seg, dir string) error {
	brackety := strings.HasPrefix(seg, "[") || strings.HasSuffix(seg, "]")
	if !brackety {
		return nil
	}
	switch {
	case seg == "[]":
		return fmt.Errorf("routing: INVALID_SEGMENT %q in %q: dynamic segment has an empty parameter name", seg, dir)
	case strings.HasPrefix(seg, "[..."):
		return fmt.Errorf("routing: INVALID_SEGMENT %q in %q: catch-all routes are not implemented yet (planned for B03); use a single [param] segment or plain static directories", seg, dir)
	case !strings.HasPrefix(seg, "[") || !strings.HasSuffix(seg, "]"):
		return fmt.Errorf("routing: INVALID_SEGMENT %q in %q: dynamic segments must be fully wrapped in brackets like [id]", seg, dir)
	case strings.Contains(seg, "[]"), strings.Contains(strings.TrimSuffix(strings.TrimPrefix(seg, "["), "]"), "["):
		return fmt.Errorf("routing: INVALID_SEGMENT %q in %q: nested brackets are not valid parameter syntax", seg, dir)
	case strings.ContainsAny(seg, "/"):
		return fmt.Errorf("routing: INVALID_SEGMENT %q in %q: parameter names cannot contain slashes", seg, dir)
	}
	name := strings.TrimSuffix(strings.TrimPrefix(seg, "["), "]")
	if strings.Contains(name, "[") || strings.Contains(name, "]") {
		return fmt.Errorf("routing: INVALID_SEGMENT %q in %q: nested brackets are not valid parameter syntax", seg, dir)
	}
	if !validParamName(name) {
		return fmt.Errorf("routing: INVALID_SEGMENT %q in %q: parameter names must start with a letter or underscore and contain only letters, digits, and underscores", seg, dir)
	}
	return nil
}

// validParamName reports whether name is a safe parameter identifier: it
// becomes a Fiber ":name" token and may surface in identifiers, so it must
// look like a Go identifier fragment.
func validParamName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// pathHasParamSegment reports whether any segment of dir (a slash-separated
// directory relative to the app root) is a dynamic [param] segment.
func pathHasParamSegment(dir string) bool {
	for _, seg := range strings.Split(dir, "/") {
		if _, ok := trimParamBrackets(seg); ok {
			return true
		}
	}
	return false
}

// sortRoutes orders routes deterministically by path. Duplicate paths (an
// error in later validation tasks) sort stably by source file as tie-break.
// Dynamic routes sort after static ones at the same position so that static
// paths register first and take priority in the router.
func sortRoutes(routes []Route) {
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routeSortKey(routes[i].Path) < routeSortKey(routes[j].Path)
		}
		return routes[i].SourceFile < routes[j].SourceFile
	})
}

// routeSortKey rewrites ":param" segments to sort after any static segment
// ("\x7f" is above all printable ASCII used in real path segments).
func routeSortKey(p string) string {
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, s := range segs {
		if strings.HasPrefix(s, ":") {
			segs[i] = "\x7f" + s[1:]
		}
	}
	return "/" + strings.Join(segs, "/")
}
