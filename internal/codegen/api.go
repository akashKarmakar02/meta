package codegen

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/akashKarmakar02/meta/internal/routing"
)

// WriteAPI generates isolated Go packages and the endpoint registry.
func WriteAPI(appDir, outDir, module string, routes []routing.APIRoute) error {
	root := filepath.Join(outDir, "generated", "apiroutes")
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	keep := map[string]bool{}
	var b strings.Builder
	b.WriteString(header + "\n\npackage routes\n\nimport (\n\"github.com/akashKarmakar02/meta/server\"\n")
	if len(routes) > 0 {
		b.WriteString("\"github.com/gofiber/fiber/v3\"\n")
	}
	for i, api := range routes {
		id := dynamicPackageID(path.Dir(api.Route.SourceFile))
		keep[id] = true
		source, err := os.ReadFile(filepath.Join(appDir, filepath.FromSlash(api.Route.SourceFile)))
		if err != nil {
			return err
		}
		// Dynamic source directories are not valid Go import paths. Their
		// ignore constraint keeps go list ./... from treating them as packages;
		// the generated copy must be compiled normally.
		source = bytes.TrimPrefix(source, []byte("//go:build ignore\n\n"))
		dir := filepath.Join(root, id)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		file := filepath.Join(dir, "route.go")
		if old, err := os.ReadFile(file); err != nil || !bytes.Equal(old, source) {
			if err := os.WriteFile(file, source, 0644); err != nil {
				return err
			}
		}
		fmt.Fprintf(&b, "api%d %q\n", i, path.Join(module, "generated/apiroutes", id))
	}
	b.WriteString(")\n\n// RegisterAPIRoutes returns the generated file-based endpoints.\nfunc RegisterAPIRoutes() []server.APIRoute {\nreturn []server.APIRoute{\n")
	for i, api := range routes {
		fmt.Fprintf(&b, "{Path:%q, SourceFile:%q, CatchAll:%q, Handlers: map[string]fiber.Handler{", api.Route.Path, api.Route.SourceFile, api.Route.CatchAll)
		for _, method := range api.Methods {
			fmt.Fprintf(&b, "%q:api%d.%s,", method, i, method)
		}
		b.WriteString("}},\n")
	}
	b.WriteString("}\n}\n")
	data, err := format.Source([]byte(b.String()))
	if err != nil {
		return err
	}
	dest := filepath.Join(outDir, "generated", "api_gen.go")
	if old, err := os.ReadFile(dest); err != nil || !bytes.Equal(old, data) {
		if err := os.WriteFile(dest, data, 0644); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() && len(name) == 65 && name[0] == 'p' && strings.Trim(name[1:], "0123456789abcdef") == "" && !keep[name] {
			if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
				return err
			}
		}
	}
	return nil
}
