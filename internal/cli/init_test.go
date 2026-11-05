package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func TestInitCreatesIndependentProject(t *testing.T) {
	framework, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "my-app")
	var out, stderr bytes.Buffer
	code := Run([]string{"init", dest, "--module", "example.net/acme/app", "--framework-dir", framework}, &out, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	for _, name := range []string{"go.mod", ".air.toml", "app/page.templ", "app/dashboard/page.svelte", "cmd/app/main.go", "frontend/entry.ts", "frontend/router.ts", "Makefile"} {
		if _, err := os.Stat(filepath.Join(dest, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	mod, err := os.ReadFile(filepath.Join(dest, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mod), "module example.net/acme/app") || !strings.Contains(string(mod), "replace "+frameworkModule) {
		t.Fatalf("go.mod: %s", mod)
	}
	if _, err := os.Stat(filepath.Join(dest, "frontend", "route-registry.generated.ts")); !os.IsNotExist(err) {
		t.Fatal("copied generated route registry")
	}
	if _, err := os.Stat(filepath.Join(dest, "frontend", "router.test.ts")); !os.IsNotExist(err) {
		t.Fatal("copied framework tests")
	}
	if code := Run([]string{"init", dest, "--framework-dir", framework}, &out, &stderr); code == 0 {
		t.Fatal("overwrote existing destination")
	}
	if got, err := os.ReadFile(filepath.Join(dest, "go.mod")); err != nil || !bytes.Equal(got, mod) {
		t.Fatal("existing project changed on retry")
	}
}

func TestInitRejectsInvalidArguments(t *testing.T) {
	framework, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init"},
		{"init", "--module"},
		{"init", filepath.Join(t.TempDir(), "app"), "--module", "example.com/../invalid", "--framework-dir", framework},
		{"init", filepath.Join(t.TempDir(), "app"), "--framework-dir", t.TempDir()},
		{"init", "one", "two"},
	} {
		var out, stderr bytes.Buffer
		if code := Run(args, &out, &stderr); code == 0 {
			t.Errorf("accepted %q", args)
		}
	}
}

func TestInitHelpOutsideProject(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := Run([]string{"init", "--help"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), "meta init") {
		t.Fatalf("exit %d: %s %s", code, out.String(), stderr.String())
	}
}

func TestInitUsesReleaseVersion(t *testing.T) {
	previous := Version
	Version = "v0.1.0-alpha.99"
	defer func() { Version = previous }()
	dest := filepath.Join(t.TempDir(), "app")
	var out, stderr bytes.Buffer
	if code := Run([]string{"init", dest}, &out, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	mod, err := os.ReadFile(filepath.Join(dest, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mod), frameworkModule+" v0.1.0-alpha.99") || strings.Contains(string(mod), "replace "+frameworkModule) {
		t.Fatalf("release go.mod: %s", mod)
	}
}

func TestInitBuildVersionSelection(t *testing.T) {
	for _, tc := range []struct {
		version  string
		modified bool
		want     string
	}{
		{"v0.2.0", false, "v0.2.0"},
		{"v0.2.0", true, ""},
		{"v0.1.0-alpha.1.0.20261105160505-92c988a7a53e+dirty", false, ""},
		{"v0.1.0-alpha.1.0.20261105160505-92c988a7a53e", false, ""},
		{"(devel)", false, ""},
	} {
		info := &debug.BuildInfo{Main: debug.Module{Path: frameworkModule, Version: tc.version}}
		if tc.modified {
			info.Settings = []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}}
		}
		if got := releaseBuildVersion(info); got != tc.want {
			t.Errorf("version %q modified %v: got %q, want %q", tc.version, tc.modified, got, tc.want)
		}
	}
}

// The generated server must compile from its own module, with no generated
// Go files committed in the framework repository.
func TestInitGeneratedProjectBuild(t *testing.T) {
	framework, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "app")
	var out, stderr bytes.Buffer
	if code := Run([]string{"init", dest, "--framework-dir", framework}, &out, &stderr); code != 0 {
		t.Fatalf("init exit %d: %s", code, stderr.String())
	}
	for _, args := range [][]string{
		{"list", "-mod=mod", "github.com/a-h/templ/cmd/templ"},
		{"tool", "templ", "generate", "-ignore-pattern", `[/\\]app[/\\].*\[|[/\\]generated[/\\]routepages[/\\]`},
		{"run", "-mod=mod", "github.com/akashKarmakar02/meta/cmd/routegen"},
		{"build", "-mod=mod", "-o", filepath.Join(dest, "app-bin"), "./cmd/app"},
	} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dest
		cmd.Env = append(os.Environ(), "GOWORK=off")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %v: %v\n%s", args, err, output)
		}
	}
}
