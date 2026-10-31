package routing

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Discover walks an fs.FS rooted at the app directory and describes every
// page route it finds. B04 scope: page.templ => SSR and page.svelte => CSR;
// layouts, ignore rules, and collision validation arrive in later tasks.
//
// Route output is deterministic: sorted by normalized path.
//
// The scanner describes files as data; it never compiles or executes them.
func Discover(fsys fs.FS) ([]Route, error) {
	var routes []Route

	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		dir, base := path.Split(p)
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

	sortRoutes(routes)
	return routes, nil
}

// classifyPage maps a filename to its render mode. Only exact page
// filenames count (binding decision #4); anything else — including other
// .templ/.svelte files — is a component, not a route.
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
