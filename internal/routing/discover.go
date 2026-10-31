package routing

import (
	"errors"
	"fmt"
	"io/fs"
)

// Discover inspects an fs.FS rooted at the app directory and describes every
// route it finds. B02 scope: only the root page file is detected; recursive
// traversal, layouts, .svelte classification, and validation rules arrive in
// later tasks.
//
// The scanner describes files as data; it never compiles or executes them.
func Discover(fsys fs.FS) ([]Route, error) {
	if _, err := fs.Stat(fsys, "page.templ"); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("routing: no routes found: app root has no page.templ")
		}
		return nil, fmt.Errorf("routing: stat root page: %w", err)
	}
	route, err := NewRoute("/", "page.templ", ModeSSR, nil)
	if err != nil {
		return nil, err
	}
	return []Route{route}, nil
}
