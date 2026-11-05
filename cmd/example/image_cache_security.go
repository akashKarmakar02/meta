package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// imageCachePath validates operator configuration before creating a cache
// directory. Existing parent symlinks are resolved; neither configured tree
// may contain the other. Configured directories remain operator-trusted.
func imageCachePath(cacheDir string, roots map[string]string) error {
	cachePath, err := resolveConfiguredPath(cacheDir)
	if err != nil {
		return fmt.Errorf("image cache path: %w", err)
	}
	for _, root := range roots {
		sourcePath, err := filepath.EvalSymlinks(root)
		if err != nil {
			return fmt.Errorf("image source root: %w", err)
		}
		sourcePath, err = filepath.Abs(sourcePath)
		if err != nil {
			return err
		}
		if pathContains(sourcePath, cachePath) || pathContains(cachePath, sourcePath) {
			return fmt.Errorf("image cache directory must not overlap a public source root")
		}
	}
	return nil
}
func pathContains(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func resolveConfiguredPath(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := path
	var suffix []string
	for {
		_, err = os.Lstat(current)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
	current, err = filepath.EvalSymlinks(current)
	if err != nil {
		return "", err
	}
	for i := len(suffix) - 1; i >= 0; i-- {
		current = filepath.Join(current, suffix[i])
	}
	return current, nil
}
