// Package routing describes file-based routes as data. It deliberately
// knows nothing about Fiber, templ, Svelte, or any HTTP concern: the
// scanner produces descriptors, and code generation consumes them.
package routing

import "fmt"

// RenderMode is how a route's page component is rendered.
type RenderMode string

const (
	// ModeSSR pages render on the server through templ (page.templ).
	ModeSSR RenderMode = "ssr"
	// ModeCSR pages render in the browser by mounting a Svelte component
	// (page.svelte).
	ModeCSR RenderMode = "csr"
)

// Valid reports whether m is one of the modes defined in this package.
//
// page.static.templ (SSG) is intentionally absent: Phase 1 rejects such
// files at build time (STATIC_NOT_IMPLEMENTED) instead of representing
// them as routes.
func (m RenderMode) Valid() bool {
	return m == ModeSSR || m == ModeCSR
}

// Layout is one layout wrapper in a route's layout chain.
type Layout struct {
	// ID is a deterministic stable identifier shared with generated code
	// and the client navigation protocol.
	ID string
	// SourceFile is the slash-normalized path of the layout file relative
	// to the app root, e.g. "dashboard/layout.svelte".
	SourceFile string
	// Mode is the rendering technology of the layout.
	Mode RenderMode
}

// Route is one discovered page route.
type Route struct {
	// Path is the normalized URL path, e.g. "/dashboard/settings".
	Path string
	// SourceFile is the slash-normalized page file path relative to the
	// app root, e.g. "dashboard/settings/page.svelte".
	SourceFile string
	// Mode is the rendering mode of the page.
	Mode RenderMode
	// Layouts is the wrapping chain, outermost first (root layout at
	// index 0, the innermost layout last).
	Layouts []Layout
	// ID is a deterministic stable identifier, shared with generated code
	// and the client navigation protocol.
	ID string
}

// NewLayout constructs a layout descriptor after basic validation.
func NewLayout(id, sourceFile string, mode RenderMode) (Layout, error) {
	if id == "" {
		return Layout{}, fmt.Errorf("routing: layout ID must not be empty")
	}
	if sourceFile == "" {
		return Layout{}, fmt.Errorf("routing: layout %q: source file must not be empty", id)
	}
	if !mode.Valid() {
		return Layout{}, fmt.Errorf("routing: layout %q: unsupported render mode %q", id, mode)
	}
	return Layout{ID: id, SourceFile: sourceFile, Mode: mode}, nil
}

// NewRoute constructs a route descriptor after basic validation. It does
// not normalize paths (URL normalization is a separate concern) nor derive
// stable IDs; both belong to discovery and generation.
func NewRoute(path, sourceFile string, mode RenderMode, layouts []Layout) (Route, error) {
	if path == "" || path[0] != '/' {
		return Route{}, fmt.Errorf("routing: route %q: path must be absolute and start with %q", sourceFile, "/")
	}
	if sourceFile == "" {
		return Route{}, fmt.Errorf("routing: route %q: source file must not be empty", path)
	}
	if !mode.Valid() {
		return Route{}, fmt.Errorf("routing: route %q: unsupported render mode %q", sourceFile, mode)
	}
	for _, l := range layouts {
		if !l.Mode.Valid() || l.ID == "" {
			return Route{}, fmt.Errorf("routing: route %q: invalid layout descriptor %+v", sourceFile, l)
		}
	}
	return Route{
		Path:       path,
		SourceFile: sourceFile,
		Mode:       mode,
		Layouts:    append([]Layout(nil), layouts...),
	}, nil
}
