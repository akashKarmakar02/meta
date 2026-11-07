package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Air watches this Go-suffixed file, while the Go tool ignores hidden files.
const routeTrigger = ".go-meta-route-trigger.go"
const routeReconcileInterval = 2 * time.Second

// routeManifest records route declarations and dynamic templ package inputs,
// not file contents. WalkDir sorts entries, so the result is deterministic.
func routeManifest(root string) (string, error) {
	var paths []string
	app := filepath.Join(root, "app")
	err := filepath.WalkDir(app, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			rel, err := filepath.Rel(app, path)
			if err != nil {
				return err
			}
			switch entry.Name() {
			case "page.templ", "page.svelte", "layout.templ", "layout.svelte", "route.go":
				paths = append(paths, rel)
			default:
				for _, segment := range strings.Split(filepath.ToSlash(filepath.Dir(rel)), "/") {
					if strings.HasPrefix(segment, "[") && strings.HasSuffix(segment, "]") &&
						(strings.HasSuffix(entry.Name(), ".templ") || strings.HasSuffix(entry.Name(), ".go")) {
						paths = append(paths, rel)
						break
					}
				}
			}
		}
		return nil
	})
	return strings.Join(paths, "\n"), err
}

// startRouteReconciler owns a single ticker for the lifetime of meta dev.
// A changed manifest writes a watched file, asking Air to run its usual build.
// Failed scans and writes leave the previous snapshot intact and are retried;
// notices print through devOutput so they carry the [meta] tag.
func startRouteReconciler(root string, errW io.Writer) (func(), error) {
	manifest, err := routeManifest(root)
	if err != nil {
		return nil, err
	}
	trigger := filepath.Join(root, routeTrigger)
	if err := os.WriteFile(trigger, []byte(manifest), 0o644); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(routeReconcileInterval)
		defer ticker.Stop()
		var requestedTree, requestedGenerated string
		requested := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				next, err := routeManifest(root)
				if err != nil {
					fmt.Fprintf(errW, "meta dev: route scan: %v\n", err)
					continue
				}
				generatedPath := filepath.Join(root, ".tmp", "air", ".route-manifest")
				generated, readErr := os.ReadFile(generatedPath)
				generatedTree := string(generated)
				generation := generatedTree
				if info, err := os.Stat(generatedPath); err == nil {
					generation += "\x00" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
				}
				// A build may finish for an intermediate tree that appeared and
				// disappeared between scans. Compare its successful snapshot too.
				outOfSync := readErr == nil && generatedTree != next
				alreadyRequested := requested && requestedTree == next && requestedGenerated == generation
				if next == manifest && (!outOfSync || alreadyRequested) {
					continue
				}
				if err := os.WriteFile(trigger, []byte(next), 0o644); err != nil {
					fmt.Fprintf(errW, "meta dev: route rebuild request: %v\n", err)
					continue
				}
				manifest = next
				requested, requestedTree, requestedGenerated = true, next, generation
			}
		}
	}()
	return func() { cancel(); <-done; _ = os.Remove(trigger) }, nil
}
