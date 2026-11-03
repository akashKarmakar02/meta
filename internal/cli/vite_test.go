package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestViteCommand(t *testing.T) {
	root := t.TempDir()
	look := func() (string, error) { return "/node", nil }
	if _, _, _, err := viteCommand(root, func() (string, error) { return "", errors.New("install Node.js") }); err == nil || !strings.Contains(err.Error(), "Node") {
		t.Fatalf("missing Node: %v", err)
	}
	if _, _, _, err := viteCommand(root, look); err == nil || !strings.Contains(err.Error(), "npm ci") {
		t.Fatalf("missing Vite: %v", err)
	}
	file := filepath.Join(root, "node_modules/vite/bin/vite.js")
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_META_VITE_PORT", "12345")
	node, args, origin, err := viteCommand(root, look)
	if err != nil || node != "/node" || origin != "http://127.0.0.1:12345" || strings.Join(args, " ") != file+" --host 127.0.0.1 --port 12345 --strictPort --base /_go_meta_vite/" {
		t.Fatalf("command: %s %q %s %v", node, args, origin, err)
	}
	t.Setenv("GO_META_VITE_PORT", "no")
	if _, _, _, err := viteCommand(root, look); err == nil {
		t.Fatal("accepted invalid port")
	}
}
