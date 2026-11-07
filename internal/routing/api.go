package routing

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// APIRoute describes a file-based HTTP endpoint discovered at build time.
type APIRoute struct {
	Route   Route
	Methods []string
}

// DiscoverAPI finds route.go files and validates their URL shapes against pages.
func DiscoverAPI(fsys fs.FS, pages []Route) ([]APIRoute, error) {
	apis := []APIRoute{}
	all := append([]Route(nil), pages...)
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := path.Base(p)
			if p != "." && (strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != "route.go" {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("routing: %s must be a regular file", p)
		}
		source, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		dynamic := pathHasParamSegment(path.Dir(p))
		if dynamic && !strings.HasPrefix(string(source), "//go:build ignore\n\n") {
			return fmt.Errorf("routing: %s: dynamic API routes must begin with //go:build ignore followed by a blank line", p)
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, source, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("routing: %s: %w", p, err)
		}
		for _, group := range f.Comments {
			for _, c := range group.List {
				if dynamic && c.Text == "//go:build ignore" {
					continue
				}
				if strings.HasPrefix(c.Text, "//go:build") || strings.HasPrefix(c.Text, "//go:embed") || strings.HasPrefix(c.Text, "// +build") {
					return fmt.Errorf("routing: %s: build and embed directives are not supported in route.go", p)
				}
			}
		}
		allowed := map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true}
		methods := []string{}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !allowed[fn.Name.Name] {
				continue
			}
			if fn.Type.TypeParams != nil || fn.Body == nil || fn.Type.Params == nil || fn.Type.Results == nil || len(fn.Type.Params.List) != 1 || len(fn.Type.Results.List) != 1 || len(fn.Type.Params.List[0].Names) > 1 || len(fn.Type.Results.List[0].Names) > 1 {
				return fmt.Errorf("routing: %s: %s must have signature func(fiber.Ctx) error", p, fn.Name.Name)
			}
			methods = append(methods, fn.Name.Name)
		}
		if len(methods) == 0 {
			return fmt.Errorf("routing: %s: define at least one HTTP method handler", p)
		}
		sort.Strings(methods)
		dir := path.Dir(p)
		route, err := NewRoute(normalizeDirToPath(dir+"/"), p, ModeSSR, nil)
		if err != nil {
			return err
		}
		if dir == "." {
			route.Path = "/"
		}
		route.CatchAll = catchAllName(dir + "/")
		for _, prefix := range []string{"/_meta", "/assets", "/_go_meta_vite", "/healthz"} {
			if route.Path == prefix || strings.HasPrefix(route.Path, prefix+"/") {
				return fmt.Errorf("routing: %s uses reserved path %s", p, prefix)
			}
		}
		all = append(all, route)
		apis = append(apis, APIRoute{Route: route, Methods: methods})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := ValidateRoutes(all); err != nil {
		return nil, err
	}
	sort.Slice(apis, func(i, j int) bool { return apis[i].Route.SourceFile < apis[j].Route.SourceFile })
	return apis, nil
}
