package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// ProjectImports identifies the owning module and app directory. Generated
// Go imports must refer to the consumer's module, not to the generator's.
func ProjectImports(appDir, root string) (modulePath, appImportDir string, err error) {
	modFile := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(modFile)
	if err != nil {
		return "", "", fmt.Errorf("codegen: read project go.mod: %w", err)
	}
	modulePath = modfile.ModulePath(data)
	if modulePath == "" {
		return "", "", fmt.Errorf("codegen: %s has no valid module directive", modFile)
	}
	if err := module.CheckImportPath(modulePath); err != nil {
		return "", "", fmt.Errorf("codegen: invalid module path %q: %w", modulePath, err)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	appAbs, err := filepath.Abs(appDir)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(rootAbs, appAbs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("codegen: app directory %q must be inside module root %q", appDir, root)
	}
	return modulePath, filepath.ToSlash(rel), nil
}
