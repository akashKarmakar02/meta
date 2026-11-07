// Command routegen generates the route registration code from the app
// tree at build time. It writes generated/routes_gen.go under the repo
// root and exits nonzero with a diagnostic on any failure.
package main

import (
	"fmt"
	"os"

	"github.com/akashKarmakar02/meta/internal/codegen"
	"github.com/akashKarmakar02/meta/internal/routing"
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
	modulePath, appImportDir, err := codegen.ProjectImports(appDir, outDir)
	if err != nil {
		return err
	}
	fsys := os.DirFS(appDir)

	routes, err := routing.Discover(fsys)
	if err != nil {
		return fmt.Errorf("discover routes in %s: %w", appDir, err)
	}

	manifest := routing.NewManifest(routes)
	apiRoutes, err := routing.DiscoverAPI(fsys, routes)
	if err != nil {
		return fmt.Errorf("discover API routes in %s: %w", appDir, err)
	}

	if err := codegen.VerifySymbols(fsys, manifest); err != nil {
		return fmt.Errorf("verify SSR page symbols in %s: %w", appDir, err)
	}
	packages, err := codegen.GenerateDynamicPackages(appDir, outDir, manifest)
	if err != nil {
		return fmt.Errorf("generate dynamic templ packages: %w", err)
	}

	if err := codegen.WriteRoutesFileForProject(outDir, manifest, modulePath, appImportDir); err != nil {
		return err
	}
	if err := codegen.WriteAPI(appDir, outDir, modulePath, apiRoutes); err != nil {
		return fmt.Errorf("generate API routes: %w", err)
	}

	if err := codegen.WriteRouteRegistryTSForProject(outDir, manifest, appImportDir); err != nil {
		return err
	}
	if err := codegen.CleanDynamicPackages(outDir, packages); err != nil {
		return err
	}

	fmt.Printf("routegen: %d routes written to %s and %s\n",
		len(manifest.Routes), "generated/routes_gen.go", "frontend/route-registry.generated.ts")
	return nil
}
