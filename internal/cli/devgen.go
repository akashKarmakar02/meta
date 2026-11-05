// P2-A03 dev generation orchestrator. Runs inside Air's build.cmd, before
// go build. Contract (blueprint A03):
//   - Generation completes before Go compilation begins (Air runs
//     build.cmd to completion; go build is part of the same cmd chain).
//   - Two generation jobs never write overlapping outputs concurrently:
//     an O_EXCL lock file with liveness check guards against a concurrent
//     `make generate` or a second meta dev.
//   - Generated writes do not retrigger rebuild loops: writers use
//     skip-if-same, so unchanged outputs keep their mtimes and only real
//     source edits produce another (desired) rebuild.
//   - A failed generation leaves the last valid generated files intact,
//     so the Go build fails loudly and Air keeps serving the previous
//     binary.
//   - Consistency recovery: fsnotify misses recursive directory deletions
//     (`rm -rf app/someroute`) entirely — no Air event fires, so no build
//     runs and routes_gen.go can dangle referencing the deleted package
//     (observed live; every later build then failed). devgen therefore
//     verifies tree/generated consistency on EVERY build it participates
//     in, and regenerates whenever the mtime scan says so OR consistency
//     fails, guaranteeing the next edit recovers the session.
package cli

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// devGenLockName serializes generation jobs across processes: Air runs one
// build at a time, but a developer can concurrently run `make generate`
// or a second meta dev in another terminal.
const devGenLockName = ".devgen.lock"

// genNeeds says which pipeline stages must run.
type genNeeds struct {
	templ  bool
	routes bool
}

// devgen runs the development generation pipeline for meta dev (P2-A03).
// It returns nil even when nothing needed regeneration, so Air proceeds to
// the plain Go build; on failure it returns the tool's real error so the
// developer sees the actual diagnostic and Air keeps the last good binary
// running.
func devgen(root string, out, errW io.Writer) error {
	needs := scanGenerationInputs(root)
	manifest, manifestErr := routeManifest(root)
	manifestPath := filepath.Join(root, ".tmp", "air", ".route-manifest")
	if manifestErr == nil {
		previous, err := os.ReadFile(manifestPath)
		if err != nil || string(previous) != manifest {
			needs.routes = true
		}
	}

	// Always verify tree/generated consistency — not only on the no-op
	// path. fsnotify drops recursive directory deletions (observed live:
	// `rm -rf app/someroute` yields no usable Air event), and a later
	// unrelated edit must not rebuild against a stale routes_gen.go that
	// still imports the deleted package.
	matches, err := generatedMatchesTree(root)
	if err != nil {
		fmt.Fprintf(errW, "devgen: consistency check skipped: %v\n", err)
		matches = true // unverifiable: fall through to the plain build
	}
	if !matches || orphanTemplGo(root) {
		fmt.Fprintln(errW, "devgen: generated output out of sync with source tree; regenerating")
		needs.templ = true
		needs.routes = true
	}

	if !needs.templ && !needs.routes {
		return nil
	}

	if err := acquireDevGenLock(root); err != nil {
		// Another devgen (a concurrent make generate, or an overlapping
		// Air build after rapid edits) holds the lock. Wait for it
		// rather than failing this build: failing would drop the edit
		// that triggered it if no further event follows. After the wait,
		// re-running devgen re-scans inputs against the just-updated
		// marker and no-ops through to the build.
		fmt.Fprintf(errW, "devgen: waiting for concurrent generation to finish\n")
		if werr := waitForDevGenLock(root, 90*time.Second); werr != nil {
			return fmt.Errorf("devgen: %w", err)
		}
		return devgen(root, out, errW)
	}
	defer releaseDevGenLock(root)

	if needs.templ {
		if err := runTool(out, errW, root, "go", "tool", "templ", "generate", "-ignore-pattern", `[/\\]app[/\\].*\[|[/\\]generated[/\\]routepages[/\\]`); err != nil {
			return fmt.Errorf("devgen: templ generate failed: %w", err)
		}
		// Dynamic templ output is compiled by routegen, including component edits.
		needs.routes = true
	}
	if needs.routes {
		if err := runTool(out, errW, root, "go", "run", "./cmd/routegen"); err != nil {
			return fmt.Errorf("devgen: route generation failed: %w", err)
		}
	}
	removeOrphanTemplGo(root, errW)
	writeGenMarker(root)
	if manifestErr == nil {
		_ = os.WriteFile(manifestPath, []byte(manifest), 0o644)
	}
	return nil
}

// devgenMarkerPath records the last successful generation. Inputs newer
// than the marker trigger regeneration; the marker is written AFTER all
// stages succeed, so a failed stage re-runs next time (no lost edits).
func devgenMarkerPath(root string) string {
	return filepath.Join(root, ".tmp", "air", ".devgen-marker")
}

// scanGenerationInputs stats the app tree and reports which stages have
// inputs newer than the last-success marker:
//
//   - templ: any .templ file changed (pages, layouts, components — all of
//     them feed templ generate).
//   - routes: route-structure files changed — page.templ/layout.templ plus
//     page.svelte/layout.svelte adds/removes/renames. Content edits to
//     .svelte route files also bump mtime; regenerating for them is cheap
//     (routegen only) and skip-if-same output means no downstream churn.
//
// Ordinary .svelte component edits (not page/layout) trigger nothing:
// they are A04's concern. Deletions that fsnotify drops entirely are
// handled by the consistency check instead.
func scanGenerationInputs(root string) genNeeds {
	markerTime := int64(0)
	if mi, err := os.Stat(devgenMarkerPath(root)); err == nil {
		markerTime = mi.ModTime().UnixNano()
	}

	var needs genNeeds
	_ = filepath.WalkDir(filepath.Join(root, "app"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // unreadable entries: leave the decision to the tools
		}
		info, serr := d.Info()
		if serr != nil {
			return nil
		}
		mtime := info.ModTime().UnixNano()
		switch d.Name() {
		case "page.svelte", "layout.svelte":
			if mtime > markerTime {
				needs.routes = true
			}
		}
		if strings.HasSuffix(d.Name(), ".templ") && mtime > markerTime {
			needs.templ = true
			if d.Name() == "page.templ" || d.Name() == "layout.templ" {
				needs.routes = true
			}
		}
		return nil
	})
	return needs
}

// writeGenMarker stamps the completion time of a successful generation.
func writeGenMarker(root string) {
	marker := devgenMarkerPath(root)
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return // best-effort: worst case the next build regenerates again
	}
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_ = f.Close()
	now := time.Now()
	_ = os.Chtimes(marker, now, now)
}

// acquireDevGenLock creates the generation lock exclusively. If a lock
// exists, its holder pid is probed: a live holder fails this attempt; a
// dead holder's stale lock is removed and creation retried once.
func acquireDevGenLock(root string) error {
	lockPath := filepath.Join(root, devGenLockName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		fmt.Fprintf(f, "%d", os.Getpid())
		_ = f.Close()
		return nil
	}
	if !os.IsExist(err) {
		return fmt.Errorf("devgen: create lock: %w", err)
	}

	data, _ := os.ReadFile(lockPath)
	var pid int
	if _, perr := fmt.Sscanf(string(data), "%d", &pid); perr == nil && pid > 0 && processAlive(pid) {
		return errors.New("devgen: another generation job is running; it will finish in a moment")
	}

	// Stale lock (holder died): remove and retry once.
	_ = os.Remove(lockPath)
	f, err = os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("devgen: create lock: %w", err)
	}
	fmt.Fprintf(f, "%d", os.Getpid())
	_ = f.Close()
	return nil
}

// processAlive reports whether pid names a live process on this host.
// POSIX only (signal 0 probe): Windows would need a different check; A03
// targets the POSIX platforms this repository develops on.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func releaseDevGenLock(root string) {
	_ = os.Remove(filepath.Join(root, devGenLockName))
}

// waitForDevGenLock blocks until the lock file disappears or the deadline
// passes. It polls rather than watching: generation jobs are seconds-long,
// and poll resolution only matters for picking up the next build quickly.
func waitForDevGenLock(root string, timeout time.Duration) error {
	lockPath := filepath.Join(root, devGenLockName)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(lockPath); os.IsNotExist(err) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("timed out waiting for a concurrent generation job")
}

// runTool runs a generation tool in root, streaming its combined output to
// out so the developer sees the tool's own diagnostics verbatim.
func runTool(out, errW io.Writer, root string, name string, arg ...string) error {
	cmd := exec.Command(name, arg...)
	cmd.Dir = root
	cmd.Stdout = out
	cmd.Stderr = errW
	return cmd.Run()
}

// orphanTemplGo reports whether any *_templ.go file has lost its .templ
// source (deleted template). templ v0.3.x leaves the generated file behind
// (observed live); without this check, such deletions go unnoticed.
func orphanTemplGo(root string) bool {
	found := false
	_ = filepath.WalkDir(filepath.Join(root, "app"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), "_templ.go") {
			return nil
		}
		src := strings.TrimSuffix(p, "_templ.go") + ".templ"
		if _, serr := os.Stat(src); os.IsNotExist(serr) {
			found = true
		}
		return nil
	})
	return found
}

// removeOrphanTemplGo deletes *_templ.go files whose .templ source is
// gone. Only *_templ.go files whose naming twin is absent are candidates —
// hand-authored sources are never touched.
func removeOrphanTemplGo(root string, errW io.Writer) {
	_ = filepath.WalkDir(filepath.Join(root, "app"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), "_templ.go") {
			return nil
		}
		src := strings.TrimSuffix(p, "_templ.go") + ".templ"
		if _, serr := os.Stat(src); os.IsNotExist(serr) {
			fmt.Fprintf(errW, "devgen: removing orphaned %s\n", p)
			_ = os.Remove(p)
		}
		return nil
	})
}

// generatedMatchesTree verifies generated/routes_gen.go is consistent with
// the current source tree: every app-package it imports must exist on
// disk. It catches the case where a route directory was deleted without a
// watcher event (fsnotify drops recursive directory deletions), which
// otherwise leaves routes_gen.go importing a missing package and fails
// every subsequent build until something regenerates it.
//
// Returns (false, nil) when inconsistent; a non-nil error only means the
// check itself could not run (missing/unparsable file).
func generatedMatchesTree(root string) (bool, error) {
	genFile := filepath.Join(root, "generated", "routes_gen.go")
	src, err := os.ReadFile(genFile)
	if err != nil {
		return false, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, genFile, src, parser.ImportsOnly)
	if err != nil {
		return false, err
	}
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		// Only app-tree imports are structural: e.g. github.com/akashKarmakar02/meta/app/docs.
		if !strings.Contains(path, "/app/") {
			continue
		}
		if !pkgExistsUnder(root, path) {
			return false, nil
		}
	}
	return true, nil
}

// pkgExistsUnder reports whether an import path like github.com/akashKarmakar02/meta/app/docs has
// a corresponding directory containing .go files under root. The module
// prefix is unknown here (each consumer project differs), so the check
// matches by path suffix: /app/<rest> directories.
func pkgExistsUnder(root, importPath string) bool {
	idx := strings.Index(importPath, "/app/")
	if idx < 0 {
		return true // not an app import; leave to the compiler
	}
	rel := importPath[idx+1:] // "app/docs"
	dir := filepath.Join(root, filepath.FromSlash(rel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return true
		}
	}
	return false
}
