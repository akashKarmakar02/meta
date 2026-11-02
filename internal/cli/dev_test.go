package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeProcess simulates a supervised Air process for unit tests.
type fakeProcess struct {
	waitErr  error
	exitCh   chan struct{} // closed when Wait should unblock
	signaled []os.Signal
	killed   bool
}

func (p *fakeProcess) Wait() error {
	<-p.exitCh
	return p.waitErr
}

func (p *fakeProcess) Signal(sig os.Signal) error {
	p.signaled = append(p.signaled, sig)
	return nil
}

func (p *fakeProcess) Kill() error {
	p.killed = true
	close(p.exitCh)
	return nil
}

// fakeRunner records the launch it was asked to perform.
type fakeRunner struct {
	proc    process
	gotName string
	gotArgs []string
	gotDir  string
	gotCtx  context.Context
	started chan struct{}
}

func (r *fakeRunner) Start(ctx context.Context, stdout io.Writer, name string, arg ...string) (process, error) {
	r.gotCtx = ctx
	r.gotName = name
	r.gotArgs = arg
	r.gotDir = workingDirFrom(ctx)
	if r.started != nil {
		close(r.started)
	}
	return r.proc, nil
}

// withTestDeps installs deps as the production seams for one test.
func withTestDeps(t *testing.T, deps devDeps) {
	t.Helper()
	old := prodDeps
	prodDeps = deps
	t.Cleanup(func() { prodDeps = old })
}

// immediateSignals returns a signal channel that never fires; tests that
// need interruption close the channel they capture from interruptAt.
func immediateSignals() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal)
	return ch, func() {}
}

// errLookAir simulates Air missing from PATH.
func errLookAir() (string, error) {
	return "", errors.New("meta dev: Air was not found in PATH.\n\nInstall Air:\n  go install github.com/air-verse/air@latest")
}

func TestDevMissingAir(t *testing.T) {
	root := makeFullProject(t)
	t.Chdir(root)

	var stdout, stderr bytes.Buffer
	ctx := &Context{Stdout: &stdout, Stderr: &stderr}
	err := runDevWith(ctx, devDeps{
		lookAir: errLookAir,
		runner:  &fakeRunner{},
		signals: immediateSignals,
		grace:   time.Second,
	})
	if err == nil {
		t.Fatal("expected missing-Air error, got nil")
	}
	if !strings.Contains(err.Error(), "Air was not found in PATH") {
		t.Errorf("error = %q, want missing-Air message", err.Error())
	}
	if !strings.Contains(err.Error(), "go install github.com/air-verse/air@latest") {
		t.Errorf("error = %q, want install hint", err.Error())
	}
}

func TestDevInvalidProjectDirectory(t *testing.T) {
	chdirTemp(t) // no project anywhere above

	var stdout, stderr bytes.Buffer
	ctx := &Context{Stdout: &stdout, Stderr: &stderr}
	err := runDevWith(ctx, devDeps{
		lookAir: errLookAir,
		runner:  &fakeRunner{},
		signals: immediateSignals,
		grace:   time.Second,
	})
	if err == nil {
		t.Fatal("expected no-project error, got nil")
	}
	if _, ok := err.(*noProjectError); !ok {
		t.Errorf("error type = %T, want *noProjectError", err)
	}
}

// TestDevPrerequisitesTable exercises prerequisite checking against a
// project missing exactly one A02 requirement at a time.
func TestDevPrerequisitesTable(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T) string // returns project root
		wantErr string
	}{
		{
			name: "missing air config",
			setup: func(t *testing.T) string {
				root := makeProject(t)
				writeClientBundle(t, root)
				return root
			},
			wantErr: "no Air configuration found",
		},
		{
			name: "missing client bundle",
			setup: func(t *testing.T) string {
				root := makeProject(t)
				writeAirConfig(t, root)
				return root
			},
			wantErr: "client bundle not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := tt.setup(t)
			t.Chdir(root)

			var stdout, stderr bytes.Buffer
			err := runDevWith(&Context{Stdout: &stdout, Stderr: &stderr}, devDeps{
				lookAir: func() (string, error) { return "/usr/bin/air", nil },
				runner:  &fakeRunner{},
				signals: immediateSignals,
				grace:   time.Second,
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
			stdoutStr := stdout.String()
			for _, want := range []string{"Project:", "Backend:", "Watching:", "Starting development backend"} {
				if strings.Contains(stdoutStr, want) {
					t.Errorf("stdout = %q, should not claim startup before prerequisite check (%q present)", stdoutStr, want)
				}
			}
		})
	}
}

// writeAirConfig writes a minimal .air.toml into root.
func writeAirConfig(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ".air.toml"), []byte("root = \".\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeClientBundle creates dist/client/.vite/manifest.json under root.
func writeClientBundle(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "dist", "client", ".vite")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDevCommandConstruction verifies the exact Air invocation: command
// name, config argument, and working directory set to the project root.
func TestDevCommandConstruction(t *testing.T) {
	root := makeFullProject(t)
	t.Chdir(root)

	fr := &fakeRunner{proc: &fakeProcess{exitCh: make(chan struct{})}}
	sigs := make(chan os.Signal, 1)

	errc := make(chan error, 1)
	go func() {
		errc <- runDevWith(&Context{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, devDeps{
			lookAir: func() (string, error) { return "/usr/local/bin/air", nil },
			runner:  fr,
			signals: func() (<-chan os.Signal, func()) { return sigs, func() {} },
			grace:   time.Second,
		})
	}()

	// Interrupt immediately after start.
	<-time.After(10 * time.Millisecond)
	sigs <- syscall.SIGINT
	if err := <-errc; err != nil {
		t.Fatalf("interrupted session should end cleanly, got %v", err)
	}

	if fr.gotName != "/usr/local/bin/air" {
		t.Errorf("command = %q, want /usr/local/bin/air", fr.gotName)
	}
	if len(fr.gotArgs) != 1 || fr.gotArgs[0] != ".air.toml" {
		t.Errorf("args = %q, want [.air.toml]", fr.gotArgs)
	}
	if fr.gotDir != root {
		t.Errorf("working dir = %q, want %q", fr.gotDir, root)
	}
}

// TestDevStartOutput verifies the startup banner and stream wiring.
func TestDevStartOutput(t *testing.T) {
	root := makeFullProject(t)
	t.Chdir(root)

	fr := &fakeRunner{proc: &fakeProcess{exitCh: make(chan struct{})}}
	var stdout, stderr bytes.Buffer

	errc := make(chan error, 1)
	go func() {
		errc <- runDevWith(&Context{Stdout: &stdout, Stderr: &stderr}, devDeps{
			lookAir: func() (string, error) { return "air", nil },
			runner:  fr,
			signals: immediateSignals,
			grace:   time.Second,
		})
	}()

	// Let the session start, then end it by closing the process.
	close(fr.proc.(*fakeProcess).exitCh)
	if err := <-errc; err != nil {
		t.Fatalf("clean exit should be clean, got %v", err)
	}

	out := stdout.String()
	for _, want := range []string{
		"Project: " + root,
		"Backend: Air",
		"Watching: Go sources",
		"Starting development backend...",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want %q", out, want)
		}
	}
}

// TestDevStartupFailure covers Air failing to spawn (binary vanished,
// permission denied, etc.).
func TestDevStartupFailure(t *testing.T) {
	root := makeFullProject(t)
	t.Chdir(root)

	var stdout, stderr bytes.Buffer
	err := runDevWith(&Context{Stdout: &stdout, Stderr: &stderr}, devDeps{
		lookAir: func() (string, error) { return "/nonexistent/air", nil },
		runner:  failRunner{},
		signals: immediateSignals,
		grace:   time.Second,
	})
	if err == nil {
		t.Fatal("expected startup failure error")
	}
	if !strings.Contains(err.Error(), "failed to start Air") {
		t.Errorf("error = %q, want failed-to-start message", err.Error())
	}
}

// failRunner always fails to Start.
type failRunner struct{}

func (failRunner) Start(ctx context.Context, stdout io.Writer, name string, arg ...string) (process, error) {
	return nil, errors.New("exec: no such file")
}

// TestDevNonzeroExit covers Air exiting with an error on its own.
func TestDevNonzeroExit(t *testing.T) {
	root := makeFullProject(t)
	t.Chdir(root)

	fp := &fakeProcess{
		exitCh:  make(chan struct{}),
		waitErr: fmt.Errorf("air: build failed"),
	}
	// waitErr is a plain error (not *exec.ExitError), exercising the
	// generic wrap branch; the typed branch is covered by the
	// integration test against real Air.
	close(fp.exitCh)

	var stdout, stderr bytes.Buffer
	err := runDevWith(&Context{Stdout: &stdout, Stderr: &stderr}, devDeps{
		lookAir: func() (string, error) { return "air", nil },
		runner:  &fakeRunner{proc: fp},
		signals: immediateSignals,
		grace:   time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "exited unexpectedly") {
		t.Fatalf("error = %v, want exited-unexpectedly message", err)
	}
}

// TestDevSignalShutdownGrace verifies graceful-then-forceful teardown:
// when Air ignores the forwarded interrupt, meta kills it after the grace
// period instead of hanging forever.
func TestDevSignalShutdownGrace(t *testing.T) {
	root := makeFullProject(t)
	t.Chdir(root)

	// Process never exits on Signal; only Kill unblocks Wait.
	fp := &fakeProcess{exitCh: make(chan struct{})}
	killOnly := &killOnlyProcess{f: fp}
	fr := &fakeRunner{proc: killOnly}

	errc := make(chan error, 1)
	sigs := make(chan os.Signal, 1)
	go func() {
		errc <- runDevWith(&Context{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, devDeps{
			lookAir: func() (string, error) { return "air", nil },
			runner:  fr,
			signals: func() (<-chan os.Signal, func()) { return sigs, func() {} },
			grace:   50 * time.Millisecond,
		})
	}()

	sigs <- syscall.SIGINT
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("interrupted session must end cleanly, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session hung past grace period; forceful kill path broken")
	}
	if !killOnly.f.killed {
		t.Error("grace period expired but process was never killed")
	}
	if len(killOnly.f.signaled) == 0 {
		t.Error("interrupt was not forwarded to the process before kill")
	}
}

// TestDevCleanExitCodeCompat pins A01 CLI behavior: meta dev failures
// still map to exit code 1 through Run.
func TestDevCleanExitCodeCompat(t *testing.T) {
	root := makeFullProject(t)
	t.Chdir(root)

	withTestDeps(t, devDeps{
		lookAir: errLookAir,
		runner:  &fakeRunner{},
		signals: immediateSignals,
		grace:   time.Second,
	})

	var stdout, stderr bytes.Buffer
	code := Run([]string{"dev"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("Run(dev) with missing Air = exit %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "Air was not found") {
		t.Errorf("stderr = %q, want missing-Air message", stderr.String())
	}
}

// killOnlyProcess ignores Signal and exits only on Kill; it exercises the
// grace-period expiry path.
type killOnlyProcess struct {
	f *fakeProcess
}

func (p *killOnlyProcess) Wait() error { return p.f.Wait() }
func (p *killOnlyProcess) Signal(sig os.Signal) error {
	p.f.signaled = append(p.f.signaled, sig)
	return nil // accepted but ignored: does not unblock Wait
}
func (p *killOnlyProcess) Kill() error { return p.f.Kill() }
