package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLISmoke builds the real meta binary once and exercises the
// end-to-end surface: flag parsing, stdout/stderr wiring, exit codes,
// and project-root discovery from a nested directory. This is the only
// test that executes the compiled CLI; the behavioral matrix lives in
// internal/cli.
func TestCLISmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("smoke test builds and runs the binary")
	}

	bin := filepath.Join(t.TempDir(), "meta")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("go build: %v", err)
	}

	// Project fixture: run meta from a nested directory to prove upward
	// discovery works in the real executable.
	root := t.TempDir()
	nested := filepath.Join(root, "app", "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		cwd      string
		args     []string
		wantCode int
		wantOut  string // substring of stdout ("" = assert stdout empty)
		wantErr  string // substring of stderr
		emptyOut bool
		emptyErr bool
	}{
		{
			name:     "help from anywhere",
			cwd:      nested,
			args:     []string{"--help"},
			wantCode: 0,
			wantOut:  "Usage:",
			emptyErr: true,
		},
		{
			name:     "version from anywhere",
			cwd:      nested,
			args:     []string{"--version"},
			wantCode: 0,
			wantOut:  "meta version dev",
			emptyErr: true,
		},
		{
			name:     "dev help topic",
			cwd:      nested,
			args:     []string{"help", "dev"},
			wantCode: 0,
			wantOut:  "meta dev",
			emptyErr: true,
		},
		{
			name:     "dev inside a bare fixture project reports missing Air configuration",
			cwd:      nested,
			args:     []string{"dev"},
			wantCode: 1,
			wantErr:  "no Air configuration found",
			emptyOut: true,
		},
		{
			name:     "unknown command exits 2",
			cwd:      nested,
			args:     []string{"bogus"},
			wantCode: 2,
			wantErr:  `unknown command "bogus"`,
			emptyOut: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(bin, tt.args...)
			cmd.Dir = tt.cwd
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()

			code := 0
			if exitErr, ok := err.(*exec.ExitError); ok {
				code = exitErr.ExitCode()
			} else if err != nil {
				t.Fatalf("run: %v", err)
			}
			if code != tt.wantCode {
				t.Errorf("exit = %d, want %d; stderr = %q", code, tt.wantCode, stderr.String())
			}
			if tt.wantOut != "" && !strings.Contains(stdout.String(), tt.wantOut) {
				t.Errorf("stdout = %q, want substring %q", stdout.String(), tt.wantOut)
			}
			if tt.wantErr != "" && !strings.Contains(stderr.String(), tt.wantErr) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), tt.wantErr)
			}
			if tt.emptyOut && stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if tt.emptyErr && stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}
