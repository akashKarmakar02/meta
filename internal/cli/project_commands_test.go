package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectCommands(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "commands")
	t.Setenv("META_TEST_COMMAND_LOG", log)
	t.Setenv("PATH", bin)
	for _, name := range []string{"go", "npm"} {
		script := "#!/bin/sh\nprintf '%s %s\\n' " + name + " \"$*\" >> \"$META_TEST_COMMAND_LOG\"\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var out, stderr bytes.Buffer
	for _, command := range []string{"setup", "generate", "build"} {
		if code := Run([]string{"--dir", root, command}, &out, &stderr); code != 0 {
			t.Fatalf("%s: %s", command, stderr.String())
		}
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, expected := range []string{"go list -mod=mod " + templToolDirective, "npm install", "go tool templ generate", "go run -mod=mod " + frameworkModule + "/cmd/routegen", "npm run build", "go build -mod=mod -o bin/app ./cmd/app"} {
		if !strings.Contains(got, expected) {
			t.Errorf("missing %q in %q", expected, got)
		}
	}
	if strings.Count(got, "go tool templ generate") != 2 {
		t.Fatalf("unexpected generation count: %s", got)
	}
}

func TestProjectCommandsFailureStopsBuild(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	var out, stderr bytes.Buffer
	if code := Run([]string{"--dir", root, "build"}, &out, &stderr); code == 0 || !strings.Contains(stderr.String(), "exit status 7") {
		t.Fatalf("code %d, stderr %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "bin")); !os.IsNotExist(err) {
		t.Fatal("build continued after generation failed")
	}
}
