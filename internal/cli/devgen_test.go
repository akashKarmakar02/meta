package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// devgenFixture builds a minimal project tree with the app/ markers that
// scanGenerationInputs and removeOrphanTemplGo walk.
func devgenFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "app-fixture")
	for _, d := range []string{"app", "app/docs"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeFixtureFile(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// past returns a time safely older than any marker written now.
func past() time.Time { return time.Now().Add(-10 * time.Minute) }

func TestScanGenerationInputs(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, root string)
		wantTempl bool
		wantRoute bool
	}{
		{
			name: "no marker and no inputs: nothing to do",
			setup: func(t *testing.T, root string) {
				writeFixtureFile(t, filepath.Join(root, "app", "readme.txt"), "x", past())
			},
		},
		{
			name: "page.templ newer than marker: templ + routes",
			setup: func(t *testing.T, root string) {
				writeMarker(t, root)
				writeFixtureFile(t, filepath.Join(root, "app", "page.templ"), "x", time.Now())
			},
			wantTempl: true,
			wantRoute: true,
		},
		{
			name: "component .templ newer: templ only",
			setup: func(t *testing.T, root string) {
				writeMarker(t, root)
				writeFixtureFile(t, filepath.Join(root, "app", "nav.templ"), "x", time.Now())
			},
			wantTempl: true,
		},
		{
			name: "page.svelte added: routes only",
			setup: func(t *testing.T, root string) {
				writeMarker(t, root)
				writeFixtureFile(t, filepath.Join(root, "app", "page.svelte"), "x", time.Now())
			},
			wantRoute: true,
		},
		{
			name: "ordinary .svelte edit: nothing (A04 owns content)",
			setup: func(t *testing.T, root string) {
				writeMarker(t, root)
				writeFixtureFile(t, filepath.Join(root, "app", "Card.svelte"), "x", time.Now())
			},
		},
		{
			name: "layout.templ in nested dir: templ + routes",
			setup: func(t *testing.T, root string) {
				writeMarker(t, root)
				writeFixtureFile(t, filepath.Join(root, "app", "docs", "layout.templ"), "x", time.Now())
			},
			wantTempl: true,
			wantRoute: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := devgenFixture(t)
			tt.setup(t, root)
			got := scanGenerationInputs(root)
			if got.templ != tt.wantTempl || got.routes != tt.wantRoute {
				t.Errorf("scan = %+v, want templ=%v routes=%v", got, tt.wantTempl, tt.wantRoute)
			}
		})
	}
}

// writeMarker stamps a successful generation in the past.
func writeMarker(t *testing.T, root string) {
	t.Helper()
	marker := devgenMarkerPath(root)
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(marker)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	old := past()
	if err := os.Chtimes(marker, old, old); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveOrphanTemplGo(t *testing.T) {
	root := devgenFixture(t)
	now := time.Now()

	// Live pair: kept. Orphan: removed. Hand-authored Go: kept.
	writeFixtureFile(t, filepath.Join(root, "app", "page.templ"), "s", now)
	writeFixtureFile(t, filepath.Join(root, "app", "page_templ.go"), "g", now)
	writeFixtureFile(t, filepath.Join(root, "app", "gone_templ.go"), "stale", now)
	writeFixtureFile(t, filepath.Join(root, "app", "hand.go"), "hand", now)

	var stderr bytes.Buffer
	removeOrphanTemplGo(root, &stderr)

	if _, err := os.Stat(filepath.Join(root, "app", "gone_templ.go")); !os.IsNotExist(err) {
		t.Error("orphaned gone_templ.go was not removed")
	}
	if _, err := os.Stat(filepath.Join(root, "app", "page_templ.go")); err != nil {
		t.Errorf("live page_templ.go removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "app", "hand.go")); err != nil {
		t.Errorf("hand-authored hand.go removed: %v", err)
	}
	if !strings.Contains(stderr.String(), "gone_templ.go") {
		t.Errorf("stderr = %q, want removal note", stderr.String())
	}
}

func TestDevGenLockLifecycle(t *testing.T) {
	root := devgenFixture(t)

	if err := acquireDevGenLock(root); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Second acquisition while the holder (this process) lives fails.
	err := acquireDevGenLock(root)
	if err == nil || !strings.Contains(err.Error(), "another generation job") {
		t.Fatalf("second acquire = %v, want in-use error", err)
	}

	releaseDevGenLock(root)
	if err := acquireDevGenLock(root); err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	releaseDevGenLock(root)
}

func TestDevGenStaleLockRecovery(t *testing.T) {
	root := devgenFixture(t)

	// Simulate a dead holder: pid that cannot exist.
	lockPath := filepath.Join(root, devGenLockName)
	if err := os.WriteFile(lockPath, []byte("999999999"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := acquireDevGenLock(root); err != nil {
		t.Fatalf("stale lock should be recoverable: %v", err)
	}
	releaseDevGenLock(root)
}

func TestDevGenNoOpSkipsTools(t *testing.T) {
	root := devgenFixture(t)
	// Inputs written first (old), marker stamped last (newer), generated
	// output present and consistent: devgen must exit without running any
	// tools.
	writeFixtureFile(t, filepath.Join(root, "app", "page.templ"), "old", past())
	writeRoutesGen(t, root, []string{"go-meta/app"}) // consistent with tree
	writeMarker(t, root)
	manifest, err := routeManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".tmp", "air", ".route-manifest"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := devgen(root, &stdout, &stderr); err != nil {
		t.Fatalf("no-op devgen failed: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("no-op stderr = %q, want empty", stderr.String())
	}
}

// writeRoutesGen writes a routes_gen.go whose imports are exactly the
// given app packages.
func writeRoutesGen(t *testing.T, root string, appImports []string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("package generated\n\nimport (\n")
	for _, imp := range appImports {
		fmt.Fprintf(&b, "\t_ %q\n", imp)
	}
	b.WriteString(")\n")
	if err := os.MkdirAll(filepath.Join(root, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "generated", "routes_gen.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDevGenRecoversFromDeletedRouteDir covers the fsnotify gap observed
// live: `rm -rf app/someroute` produces no watcher event, so no rebuild
// runs — but the next build's consistency check must detect that
// routes_gen.go references a package that no longer exists and regenerate.
func TestDevGenRecoversFromDeletedRouteDir(t *testing.T) {
	root := devgenFixture(t)

	// A stale route reference: generated output imports app/gone, which
	// has no sources on disk.
	writeFixtureFile(t, filepath.Join(root, "app", "page.templ"), "src", past())
	writeRoutesGen(t, root, []string{"go-meta/app", "go-meta/app/gone"})
	writeMarker(t, root)

	var stdout, stderr bytes.Buffer
	// devgen cannot complete routegen in this bare fixture (no toolchain
	// inputs), so we assert only that inconsistency was detected and led
	// to a lock + regeneration attempt rather than a silent no-op exit.
	err := devgen(root, &stdout, &stderr)
	if err == nil && stderr.Len() == 0 {
		t.Fatal("inconsistent generated output accepted silently; expected regeneration attempt")
	}
	if !strings.Contains(stderr.String(), "out of sync") && !strings.Contains(err.Error(), "generate failed") {
		t.Errorf("stderr = %q err = %v; want out-of-sync message or generation attempt", stderr.String(), err)
	}
}
