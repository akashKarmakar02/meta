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
			if err := validateSegment(base, p); err != nil {
				return err
			}
			// B03: a catch-all must be the final path segment. Reject a
			// non-terminal "[...name]" directory during the walk so it never
			// reaches generated output (e.g. docs/[...slug]/extra/).
			if bad := catchAllNonTerminalSegment(p); bad != "" {
				return fmt.Errorf("routing: INVALID_SEGMENT %q in %q: catch-all segments must be the last path segment", bad, p)
			}
			return nil
		}
		dir, base := path.Split(p)
		if pathHasParamSegment(path.Dir(p)) && strings.HasSuffix(base, ".go") {
			return fmt.Errorf("routing: Go source %q is inside a dynamic route directory; move helpers to a Go-valid package and import them", p)
		}

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
		route, rerr := NewRoute(normalizeDirToPath(dir), p, mode, nil)
		if rerr != nil {
			return rerr
		}
		route.CatchAll = catchAllName(dir)
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
		if _, ok := trimCatchAllBrackets(seg); ok {
			// Fiber's one-or-more greedy segment is the bare "+" token; the
			// declared name is carried separately on Route.CatchAll (B03).
			b.WriteString("+")
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
	if strings.HasPrefix(seg, "[...") {
		return "", false // catch-all segment; handled by trimCatchAllBrackets
	}
	name := strings.TrimSuffix(strings.TrimPrefix(seg, "["), "]")
	if name == "" || strings.ContainsAny(name, "[]/") {
		return "", false
	}
	return name, true
}

// trimCatchAllBrackets reports whether seg is a catch-all segment of the form
// "[...name]" and returns the bare parameter name.
func trimCatchAllBrackets(seg string) (string, bool) {
	const prefix = "[..."
	if !strings.HasPrefix(seg, prefix) || !strings.HasSuffix(seg, "]") {
		return "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(seg, prefix), "]")
	if name == "" || strings.ContainsAny(name, "[]/...") {
		return "", false
	}
	return name, true
}

// catchAllName returns the declared catch-all parameter name if the final
// segment of dir (a slash-separated page directory) is a "[...name]" segment,
// else "".
func catchAllName(dir string) string {
	segs := strings.Split(strings.TrimSuffix(dir, "/"), "/")
	if len(segs) == 0 {
		return ""
	}
	name, ok := trimCatchAllBrackets(segs[len(segs)-1])
	if !ok {
		return ""
	}
	return name
}

// catchAllNonTerminalSegment returns the first catch-all "[...name]" segment
// of dir (a slash-separated page directory) that is not the final segment, or
// "" when every catch-all segment is terminal. A non-terminal catch-all is
// rejected: a catch-all must be the last path segment (B03).
func catchAllNonTerminalSegment(dir string) string {
	segs := strings.Split(strings.TrimSuffix(dir, "/"), "/")
	for i, seg := range segs {
		if i == len(segs)-1 {
			continue
		}
		if _, ok := trimCatchAllBrackets(seg); ok {
			return seg
		}
	}
	return ""
}

// validateSegment rejects directory names that declare bracket intent but do
// not form a valid Phase 1 dynamic parameter segment. It runs before
// normalizeDirToPath so malformed segments never reach generated output.
func validateSegment(seg, dir string) error {
	brackety := strings.HasPrefix(seg, "[") || strings.HasSuffix(seg, "]")
	if !brackety {
		if strings.ContainsAny(seg, ":*+?\\") {
			return fmt.Errorf("routing: INVALID_SEGMENT %q in %q: static segments cannot contain Fiber route metacharacters", seg, dir)
		}
		return nil
	}
	switch {
	case seg == "[]":
		return fmt.Errorf("routing: INVALID_SEGMENT %q in %q: dynamic segment has an empty parameter name", seg, dir)
	case strings.HasPrefix(seg, "[..."):
		// B03: catch-all segment. Validate the "[...name]" form; the terminal
		// position is enforced by the caller (catchAllNotTerminal).
		if !strings.HasSuffix(seg, "]") {
			return fmt.Errorf("routing: INVALID_SEGMENT %q in %q: catch-all segments must be fully wrapped in brackets like [...slug]", seg, dir)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(seg, "[..."), "]")
		if name == "" {
			return fmt.Errorf("routing: INVALID_SEGMENT %q in %q: catch-all segment has an empty parameter name", seg, dir)
		}
		if strings.ContainsAny(name, "[]/...") {
			return fmt.Errorf("routing: INVALID_SEGMENT %q in %q: nested or dotted brackets are not valid catch-all syntax", seg, dir)
		}
		if !validParamName(name) {
			return fmt.Errorf("routing: INVALID_SEGMENT %q in %q: catch-all parameter names must start with a letter or underscore and contain only letters, digits, and underscores", seg, dir)
		}
		return nil
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
		if _, ok := trimCatchAllBrackets(seg); ok {
			return true
		}
	}
	return false
}

// sortRoutes orders registrations by the first differing segment: static,
// named parameter, then catch-all. Parameter names do not affect specificity.
// Literal and source ordering break ties deterministically for disjoint routes
// and for invalid equivalent declarations awaiting validation.
func sortRoutes(routes []Route) {
	sort.Slice(routes, func(i, j int) bool {
		a, b := strings.Split(strings.TrimPrefix(routes[i].Path, "/"), "/"), strings.Split(strings.TrimPrefix(routes[j].Path, "/"), "/")
		for n := 0; n < len(a) && n < len(b); n++ {
			ar, br := segmentRank(a[n]), segmentRank(b[n])
			if ar != br {
				return ar < br
			}
			if ar == 0 && a[n] != b[n] {
				return a[n] < b[n]
			}
		}
		if len(a) != len(b) {
			return len(a) < len(b)
		}
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].SourceFile < routes[j].SourceFile
	})
}

func segmentRank(segment string) int {
	if segment == "+" {
		return 2
	}
	if strings.HasPrefix(segment, ":") {
		return 1
	}
	return 0
}

// FiberRegistrationPath encodes non-ASCII literals for Fiber's default
// escaped-path matcher. Parameter syntax and public route metadata stay intact.
func FiberRegistrationPath(p string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for i := 0; i < len(p); i++ {
		b := p[i]
		if b < 0x80 {
			out.WriteByte(b)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hex[b>>4])
		out.WriteByte(hex[b&15])
	}
	return out.String()
}

// SortRoutes orders routes by the same precedence used during discovery.
func SortRoutes(routes []Route) { sortRoutes(routes) }
