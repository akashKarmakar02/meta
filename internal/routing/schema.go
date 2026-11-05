// Package routing discovers and validates file-based routes.
package routing

import public "github.com/akashKarmakar02/meta/routing"

// These aliases keep discovery and the runtime on the same route model as
// generated code and applications importing the public routing package.
type RenderMode = public.RenderMode
type Layout = public.Layout
type Route = public.Route

const (
	ModeSSR = public.ModeSSR
	ModeCSR = public.ModeCSR
)

func NewLayout(id, sourceFile string, mode RenderMode) (Layout, error) {
	return public.NewLayout(id, sourceFile, mode)
}

func NewRoute(path, sourceFile string, mode RenderMode, layouts []Layout) (Route, error) {
	return public.NewRoute(path, sourceFile, mode, layouts)
}
