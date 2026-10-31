package routing

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Discover walks an fs.FS rooted at the app directory and describes every
// page route it finds. B06 scope: page.templ/page.svelte classification
// plus layout.templ discovery and ancestry resolution (outermost ->
// innermost). Nested layout.svelte ancestry arrives with B07; ignore rules
// and collision validation arrive in later tasks.
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
			return nil
		}
		dir, base := path.Split(p)

		if mode, ok := classifyLayout(base); ok {
			layout, lerr := NewLayout(p, p, mode)
			if lerr != nil {
				return lerr
			}
			key := strings.TrimSuffix(dir, "/")
			if key == "" {
				key = "."
			}
			if _, dup := layouts[key]; dup {
				return fmt.Errorf("routing: conflicting layout files in directory %q", key)
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
	return routes, nil
}

// layoutChain resolves the layout ancestry for a route whose page lives in
// routeDir: every ancestor directory (root first, page directory last) that
// holds a layout contributes one entry, ordered outermost -> innermost.
func layoutChain(routeDir string, layouts map[string]Layout) []Layout {
	var chain []Layout
	for dir := routeDir; ; dir = path.Dir(dir) {
		if l, ok := layouts[dir]; ok {
			chain = append(chain, l)
		}
		if dir == "." {
			break
		}
	}
	// Collected innermost-first; reverse to outermost-first.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
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

// classifyLayout maps a layout filename to its render mode. B06 recognizes
// only layout.templ; layout.svelte classification arrives with B07.
func classifyLayout(base string) (RenderMode, bool) {
	switch base {
	case "layout.templ":
		return ModeSSR, true
	default:
		return "", false
	}
}

// normalizeDirToPath converts a slash directory path like "about/" into a
// URL path like "/about". The root directory "" maps to "/".
func normalizeDirToPath(dir string) string {
	trimmed := strings.TrimSuffix(dir, "/")
	if trimmed == "" || trimmed == "." {
		return "/"
	}
	return "/" + trimmed
}

// sortRoutes orders routes deterministically by path. Duplicate paths (an
// error in later validation tasks) sort stably by source file as tie-break.
func sortRoutes(routes []Route) {
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].SourceFile < routes[j].SourceFile
	})
}
