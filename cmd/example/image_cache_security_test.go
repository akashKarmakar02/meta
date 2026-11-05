package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestF06CacheRootIsolation(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "public")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(parent, "public-other", "cache")
	if err := imageCachePath(sibling, map[string]string{"/images/": root}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{root, filepath.Join(root, "cache"), parent} {
		if err := imageCachePath(bad, map[string]string{"/images/": root}); err == nil {
			t.Fatalf("overlap accepted %s", bad)
		}
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if err := imageCachePath(filepath.Join(alias, "cache"), map[string]string{"/images/": root}); err == nil {
		t.Fatal("symlink-parent overlap accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "cache")); !os.IsNotExist(err) {
		t.Fatal("validation created cache directory")
	}
}
