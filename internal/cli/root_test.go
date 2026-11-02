package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		stdoutSub  string // substring expected on stdout ("" = no assertion)
		stderrSub  string // substring expected on stderr
		wantStdout bool   // assert stdout is empty
	}{
		{
			name:      "no args prints usage",
			args:      nil,
			wantCode:  0,
			stdoutSub: "Usage:",
		},
		{
			name:      "long help flag",
			args:      []string{"--help"},
			wantCode:  0,
			stdoutSub: "Usage:",
		},
		{
			name:      "short help flag",
			args:      []string{"-h"},
			wantCode:  0,
			stdoutSub: "Usage:",
		},
		{
			name:      "help command",
			args:      []string{"help"},
			wantCode:  0,
			stdoutSub: "Usage:",
		},
		{
			name:      "version flag",
			args:      []string{"--version"},
			wantCode:  0,
			stdoutSub: "meta version dev",
		},
		{
			name:      "dev help via help command",
			args:      []string{"help", "dev"},
			wantCode:  0,
			stdoutSub: "meta dev",
		},
		{
			name:      "dev help topic for unknown command fails",
			args:      []string{"help", "nonexistent"},
			wantCode:  2,
			stderrSub: `no help topic for "nonexistent"`,
		},
		{
			name:      "unknown command",
			args:      []string{"frobnicate"},
			wantCode:  2,
			stderrSub: `unknown command "frobnicate"`,
		},
		{
			name:       "unsupported flag",
			args:       []string{"--definitely-not-a-flag"},
			wantCode:   2,
			stderrSub:  "flag provided but not defined",
			wantStdout: true,
		},
		{
			name:       "bad help arity",
			args:       []string{"help", "dev", "extra"},
			wantCode:   2,
			stderrSub:  "usage: meta help [command]",
			wantStdout: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(tt.args, &stdout, &stderr)

			if code != tt.wantCode {
				t.Errorf("Run(%q) exit = %d, want %d", tt.args, code, tt.wantCode)
			}
			if tt.stdoutSub != "" && !strings.Contains(stdout.String(), tt.stdoutSub) {
				t.Errorf("stdout = %q, want substring %q", stdout.String(), tt.stdoutSub)
			}
			if tt.stderrSub != "" && !strings.Contains(stderr.String(), tt.stderrSub) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), tt.stderrSub)
			}
			if tt.wantStdout && stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

// TestRunHelpOutsideProject verifies the spec's requirement that help
// and version work with no valid project anywhere above the cwd. The
// HOME override plus a fresh temp cwd leave nowhere to find a project.
func TestRunHelpOutsideProject(t *testing.T) {
	cwd := chdirTemp(t)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "meta version dev\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	stdout.Reset()
	if code := Run([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Errorf("stdout missing usage: %q", stdout.String())
	}
	_ = cwd
}

func TestVersionOverride(t *testing.T) {
	old := Version
	Version = "v9.9.9-test"
	defer func() { Version = old }()

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "meta version v9.9.9-test\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestNotImplementedError(t *testing.T) {
	err := &notImplementedError{command: "dev"}
	if err.Error() == "" {
		t.Fatal("Error() should not be empty")
	}
	// Zero value must not count as an error: an accidental nil-typed
	// pointer would fail silently otherwise.
	var zero *notImplementedError
	if zero != nil {
		t.Fatal("nil *notImplementedError must be nil")
	}
}

// chdirTemp creates an empty temp dir and makes it the working directory
// for the test; t.Chdir restores it on cleanup.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

// makeProject creates a minimal valid go-meta project tree under a temp
// dir and returns its root path.
func makeProject(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "myapp")
	for _, d := range []string{"app", "internal"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/myapp\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFindRootUpwardFromProjectRoot(t *testing.T) {
	root := makeProject(t)
	t.Chdir(root)

	got, err := findRootUpward(".")
	if err != nil {
		t.Fatalf("findRootUpward: %v", err)
	}
	if want := root; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("result %q not absolute", got)
	}
}

func TestFindRootUpwardFromNestedDirectory(t *testing.T) {
	root := makeProject(t)
	nested := filepath.Join(root, "internal", "deep", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	got, err := findRootUpward(".")
	if err != nil {
		t.Fatalf("findRootUpward: %v", err)
	}
	if got != root {
		t.Errorf("got %q, want %q", got, root)
	}
}

func TestFindRootUpwardRejectsUnrelatedGoModule(t *testing.T) {
	// A plain Go module without app/: an ambient repo, not a go-meta
	// project. The pair rule must reject it.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module unrelated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	_, err := findRootUpward(".")
	if err == nil {
		t.Fatal("expected error for go.mod-only directory, got none")
	}
	if _, ok := err.(*noProjectError); !ok {
		t.Errorf("error type = %T, want *noProjectError", err)
	}
}

func TestFindRootUpwardNoProject(t *testing.T) {
	chdirTemp(t)

	_, err := findRootUpward(".")
	if err == nil {
		t.Fatal("expected error outside any project, got none")
	}
	if _, ok := err.(*noProjectError); !ok {
		t.Errorf("error type = %T, want *noProjectError", err)
	}
}

// noAppDir creates a temp directory containing only go.mod (a plain Go
// module, not a go-meta project).
func noAppDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestValidateRootTable(t *testing.T) {
	root := makeProject(t)

	tests := []struct {
		name    string
		dir     string
		wantErr string // "" means expect success
	}{
		{name: "valid root", dir: root},
		{name: "missing directory", dir: filepath.Join(t.TempDir(), "nope"), wantErr: "cannot read directory"},
		{name: "file instead of directory", dir: filepath.Join(root, "go.mod"), wantErr: "not a directory"},
		{name: "project without app dir", dir: noAppDir(t), wantErr: "not a go-meta project"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateRoot(tt.dir)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != root {
					t.Errorf("got %q, want %q", got, root)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got success with root %q", tt.wantErr, got)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestValidateRootRelativePath(t *testing.T) {
	root := makeProject(t)
	parent := filepath.Dir(root)
	t.Chdir(parent) // resolve the relative --dir against this

	got, err := validateRoot(filepath.Base(root))
	if err != nil {
		t.Fatalf("validateRoot(relative): %v", err)
	}
	if want := filepath.Clean(root); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRunDevNotImplemented(t *testing.T) {
	root := makeProject(t)
	t.Chdir(root)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"dev"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr = %q", code, stderr.String())
	}
	got := stderr.String()
	if !strings.Contains(got, "meta dev") {
		t.Errorf("stderr %q missing command prefix", got)
	}
	if !strings.Contains(got, "not implemented yet") {
		t.Errorf("stderr %q missing not-implemented marker", got)
	}
	if !strings.Contains(got, "P2-A02") {
		t.Errorf("stderr %q missing task reference", got)
	}
	if !strings.Contains(got, root) {
		t.Errorf("stderr %q missing detected root %q", got, root)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunDevOutsideProject(t *testing.T) {
	chdirTemp(t)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"dev"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "no go-meta project found") {
		t.Errorf("stderr = %q, want no-project message", stderr.String())
	}
}

func TestRunDevWithDirFlag(t *testing.T) {
	root := makeProject(t)

	tests := []struct {
		name     string
		args     []string
		wantCode int
		check    func(t *testing.T, stderr string)
	}{
		{
			name:     "valid --dir reaches not-implemented",
			args:     []string{"--dir", root, "dev"},
			wantCode: 1,
			check: func(t *testing.T, stderr string) {
				if !strings.Contains(stderr, "not implemented yet") {
					t.Errorf("stderr = %q, want not-implemented", stderr)
				}
			},
		},
		{
			name:     "invalid --dir fails fast",
			args:     []string{"--dir", filepath.Join(t.TempDir(), "missing"), "dev"},
			wantCode: 1,
			check: func(t *testing.T, stderr string) {
				if !strings.Contains(stderr, "not a usable project root") {
					t.Errorf("stderr = %q, want invalid-root message", stderr)
				}
			},
		},
		{
			name:     "extra dev argument rejected",
			args:     []string{"dev", "--watch"},
			wantCode: 1,
			check: func(t *testing.T, stderr string) {
				if !strings.Contains(stderr, `unexpected argument "--watch"`) {
					t.Errorf("stderr = %q, want unexpected-argument message", stderr)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Fatalf("exit = %d, want %d; stderr = %q", code, tt.wantCode, stderr.String())
			}
			tt.check(t, stderr.String())
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}
