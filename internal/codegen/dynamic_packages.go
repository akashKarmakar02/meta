package codegen

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"go-meta/internal/routing"
)

// GenerateDynamicPackages compiles templ files from dynamic source packages in
// a temporary tree, then publishes their generated Go into legal import paths.
// It never writes Go files into the source app tree.
func GenerateDynamicPackages(appDir, outDir string, manifest routing.Manifest) ([]string, error) {
	dirs := map[string]string{}
	add := func(source string) error {
		dir := path.Dir(source)
		if !dynamicSourceDir(dir) {
			return nil
		}
		id := dynamicPackageID(dir)
		if old, ok := dirs[id]; ok && old != dir {
			return fmt.Errorf("codegen: dynamic package identity collision: %q and %q", old, dir)
		}
		dirs[id] = dir
		return nil
	}
	for _, route := range manifest.Routes {
		if route.Mode == routing.ModeSSR {
			if err := add(route.SourceFile); err != nil {
				return nil, err
			}
		}
		for _, layout := range route.Layouts {
			if layout.Mode == routing.ModeSSR {
				if err := add(layout.SourceFile); err != nil {
					return nil, err
				}
			}
		}
	}
	ids := make([]string, 0, len(dirs))
	for id := range dirs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	if len(ids) == 0 {
		return ids, nil
	}
	tmp, err := os.MkdirTemp("", "go-meta-templ-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	for _, id := range ids {
		sourceDir := filepath.Join(appDir, filepath.FromSlash(dirs[id]))
		entries, err := os.ReadDir(sourceDir)
		if err != nil {
			return nil, fmt.Errorf("codegen: read %s: %w", sourceDir, err)
		}
		dest := filepath.Join(tmp, id)
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".templ") {
				continue
			}
			if !entry.Type().IsRegular() {
				return nil, fmt.Errorf("codegen: templ source %s must be a regular file", filepath.Join(sourceDir, entry.Name()))
			}
			data, err := os.ReadFile(filepath.Join(sourceDir, entry.Name()))
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(dest, entry.Name()), data, 0o644); err != nil {
				return nil, err
			}
		}
	}
	cmd := exec.Command("go", "tool", "templ", "generate", "-path", tmp)
	cmd.Dir = outDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("codegen: compile dynamic templ packages (sources: %v): %w\n%s", dirs, err, output)
	}
	for _, id := range ids {
		from := filepath.Join(tmp, id)
		to := filepath.Join(outDir, "generated", "routepages", id)
		if err := os.MkdirAll(to, 0o755); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(from)
		if err != nil {
			return nil, err
		}
		wanted := map[string]bool{}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), "_templ.go") {
				continue
			}
			wanted[entry.Name()] = true
			data, err := os.ReadFile(filepath.Join(from, entry.Name()))
			if err != nil {
				return nil, err
			}
			dst := filepath.Join(to, entry.Name())
			if old, err := os.ReadFile(dst); err == nil && bytes.Equal(old, data) {
				continue
			}
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				return nil, err
			}
		}
		existing, err := os.ReadDir(to)
		if err != nil {
			return nil, err
		}
		for _, entry := range existing {
			if strings.HasSuffix(entry.Name(), "_templ.go") && !wanted[entry.Name()] {
				if err := os.Remove(filepath.Join(to, entry.Name())); err != nil {
					return nil, err
				}
			}
		}
	}
	return ids, nil
}

// CleanDynamicPackages removes obsolete generator-owned packages after the
// route registry has been successfully written.
func CleanDynamicPackages(outDir string, keep []string) error {
	root := filepath.Join(outDir, "generated", "routepages")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || len(name) != 65 || name[0] != 'p' || strings.Trim(name[1:], "0123456789abcdef") != "" || slices.Contains(keep, name) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			return err
		}
	}
	return nil
}
