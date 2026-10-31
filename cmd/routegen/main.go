// Command routegen generates the route registration code from the app
// tree at build time. It writes generated/routes_gen.go under the repo
// root and exits nonzero with a diagnostic on any failure.
package main

import (
	"fmt"
	"os"

	"go-meta/internal/codegen"
	"go-meta/internal/routing"
)

func main() {
	// Arguments: [appDir] [outDir]. Defaults follow the repository layout:
	// app pages live in ./app, generated code lands in ./generated.
	appDir := "app"
	outDir := "."
	if len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "usage: routegen [appDir] [outDir]")
		os.Exit(2)
	}
	if len(os.Args) >= 2 {
		appDir = os.Args[1]
	}
	if len(os.Args) == 3 {
		outDir = os.Args[2]
	}

	if err := run(appDir, outDir); err != nil {
		fmt.Fprintf(os.Stderr, "routegen: %v\n", err)
		os.Exit(1)
	}
}

func run(appDir, outDir string) error {
	fsys := os.DirFS(appDir)

	routes, err := routing.Discover(fsys)
	if err != nil {
		return fmt.Errorf("discover routes in %s: %w", appDir, err)
	}

	manifest := routing.NewManifest(routes)

	if err := codegen.VerifySymbols(fsys, manifest); err != nil {
		return fmt.Errorf("verify SSR page symbols in %s: %w", appDir, err)
	}

	if err := codegen.WriteRoutesFile(outDir, manifest); err != nil {
		return err
	}

	if err := codegen.WriteRouteRegistryTS(outDir, manifest); err != nil {
		return err
	}

	fmt.Printf("routegen: %d routes written to %s and %s\n",
		len(manifest.Routes), "generated/routes_gen.go", "frontend/route-registry.generated.ts")
	return nil
}
