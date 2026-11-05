package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// projectRoot resolves the meta project root for the invocation.
//
// Resolution order:
//  1. An explicit --dir option, validated by validateRoot, which must
//     itself be a valid project root (fail fast on typos instead of
//     silently discovering elsewhere).
//  2. findRootUpward from the current working directory.
//
// The returned path is absolute and cleaned; discovery never changes the
// process working directory.
//
// Provisional marker rule (to be refined when meta init exists in Phase
// H): "go.mod + app/" is the current best-available signal, but it can
// still match a foreign Go project that happens to have an app/ directory.
// A dedicated marker file will replace it once init can create one.
func (c *Context) projectRoot() (string, error) {
	if c.dir != "" {
		return validateRoot(c.dir)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("meta: cannot determine working directory: %w", err)
	}
	return findRootUpward(cwd)
}

// findRootUpward searches from dir (usually the working directory) toward
// the filesystem root for the nearest meta project root. It rejects
// the ambient-Go-module case: a bare go.mod without an app/ directory is
// not accepted, so running meta inside an unrelated Go repository fails
// instead of adopting it.
func findRootUpward(from string) (string, error) {
	dir := filepath.Clean(from)
	if !filepath.IsAbs(dir) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("meta: cannot determine working directory: %w", err)
		}
		dir = filepath.Join(cwd, dir)
	}
	for {
		if isProjectRoot(dir) {
			return filepath.Clean(dir), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		dir = parent
	}
	return "", &noProjectError{searchedFrom: from}
}

// validateRoot validates an explicitly supplied root (--dir). A relative
// path is resolved against the working directory so the returned value
// is always absolute; an unusable directory is an error rather than a
// trigger for silent discovery elsewhere.
func validateRoot(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("meta: cannot determine working directory: %w", err)
		}
		dir = filepath.Join(cwd, dir)
	}
	dir = filepath.Clean(dir)

	info, err := os.Stat(dir)
	if err != nil {
		return "", &invalidProjectError{dir: dir, reason: "cannot read directory"}
	}
	if !info.IsDir() {
		return "", &invalidProjectError{dir: dir, reason: "not a directory"}
	}
	if !isProjectRoot(dir) {
		return "", &invalidProjectError{dir: dir, reason: "missing go.mod or app/ directory (not a meta project)"}
	}
	return dir, nil
}

// isProjectRoot reports whether dir looks like a meta project root:
// a go.mod file plus an app directory, per the provisional marker rule
// documented on Context.projectRoot.
func isProjectRoot(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, "app"))
	return err == nil && info.IsDir()
}

// noProjectError means no project root was found by upward search.
type noProjectError struct{ searchedFrom string }

func (e *noProjectError) Error() string {
	return fmt.Sprintf(
		"meta: no meta project found (searched upward from %s); run from inside a project or pass --dir <path>",
		e.searchedFrom,
	)
}

// invalidProjectError means an explicitly supplied root failed validation.
type invalidProjectError struct {
	dir    string
	reason string
}

func (e *invalidProjectError) Error() string {
	return fmt.Sprintf("meta: %s is not a usable project root: %s", e.dir, e.reason)
}
