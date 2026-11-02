package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func init() {
	register(&Command{
		Name:  "dev",
		Short: "Start the development server",
		Usage: `Start the development server.

meta dev discovers the project root, checks prerequisites, and runs the
Go backend under Air (github.com/air-verse/air) for automatic rebuild
and restart on Go source changes (P2-A02).

Current scope:
  - Go backend hot reload via Air only.
  - templ and route metadata regenerate before backend builds.
  - Vite/Svelte HMR and the unified dev origin arrive in A04/A05; the
    client bundle under dist/client must be built once beforehand
    ('make build' or 'npm run build') or the server exits with a
    missing-assets error.

Usage:
  meta dev [--dir <path>]

Flags:
  --dir <path>    Project root directory (skips discovery)

Environment:
  PORT            Server port override (default :3000), passed through
                  to the server process

Exit status:
  0  interrupted development session (Ctrl+C)
  1  missing Air executable, missing Air configuration, missing client
     bundle, Air failing to start or exiting unexpectedly, or an invalid
     project root.
`,
		Run: runDev,
	})
}

// airShutdownGrace bounds how long meta waits for Air to tear down after
// an interrupt before killing it outright.
const airShutdownGrace = 5 * time.Second

// process is the supervisee side of the launch seam: minimal surface the
// dev command needs, implemented by *osProcess in production and fakes
// in unit tests.
type process interface {
	Wait() error
	Signal(os.Signal) error
	Kill() error
}

// runner launches processes with streamed output and a working directory.
type runner interface {
	Start(ctx context.Context, stdout io.Writer, name string, arg ...string) (process, error)
}

// osProcess wraps an *exec.Cmd started by execRunner.
type osProcess struct{ cmd *exec.Cmd }

func (p *osProcess) Wait() error { return p.cmd.Wait() }

func (p *osProcess) Signal(sig os.Signal) error { return p.cmd.Process.Signal(sig) }

func (p *osProcess) Kill() error { return p.cmd.Process.Kill() }

// execRunner starts real subprocesses.
type execRunner struct{}

// Start launches name in the working directory carried by ctx (see
// withWorkingDir), stdout and stderr both wired to stdout so Air's
// interleaved watcher/build/server logs stream through unbuffered. The
// parent environment is inherited, so PATH/GOPATH/GOFLAGS/PORT flow to
// Air and the server normally.
func (execRunner) Start(ctx context.Context, stdout io.Writer, name string, arg ...string) (process, error) {
	cmd := exec.Command(name, arg...)
	cmd.Dir = workingDirFrom(ctx)
	cmd.Stdout = stdout
	cmd.Stderr = stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &osProcess{cmd: cmd}, nil
}

type ctxKey int

const ctxKeyWorkingDir ctxKey = iota

func withWorkingDir(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, ctxKeyWorkingDir, dir)
}

func workingDirFrom(ctx context.Context) string {
	dir, _ := ctx.Value(ctxKeyWorkingDir).(string)
	return dir
}

// devDeps bundles the seams behind meta dev so the session logic is
// unit-testable without a real Air binary: resolving Air, launching
// processes, and receiving interruption signals.
type devDeps struct {
	lookAir func() (string, error)
	runner  runner
	signals func() (<-chan os.Signal, func())
	grace   time.Duration // override for the shutdown grace period in tests
}

// prodDeps is the production wiring. Tests may swap this variable (with
// cleanup) rather than threading a second parameter through runDev.
var prodDeps = devDeps{
	lookAir: airLookPath,
	runner:  execRunner{},
	signals: watchInterrupt,
	grace:   airShutdownGrace,
}

// watchInterrupt subscribes to termination signals.
func watchInterrupt() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	return ch, func() { signal.Stop(ch) }
}

// airLookPath resolves the Air executable. It tries PATH first, then the
// Go bin directory ($GOPATH/bin or GOBIN) where `go install` places it —
// developers frequently lack that directory on PATH. Absent everywhere,
// it returns an actionable installation hint.
func airLookPath() (string, error) {
	if path, err := exec.LookPath("air"); err == nil {
		return path, nil
	}
	if gobin := goBinDir(); gobin != "" {
		candidate := filepath.Join(gobin, "air")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf(`meta dev: Air was not found in PATH.

Install Air:
  go install github.com/air-verse/air@latest`)
}

// goBinDir reports where go install places executables, or "" when
// unknowable.
func goBinDir() string {
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		return gobin
	}
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		out, err := exec.Command("go", "env", "GOPATH").Output()
		if err != nil {
			return ""
		}
		gopath = string(bytes.TrimSpace(out))
	}
	first := filepath.SplitList(gopath)
	if len(first) == 0 || first[0] == "" {
		return ""
	}
	return filepath.Join(first[0], "bin")
}

// findAirConfig looks for the project's Air configuration at the project
// root: committed .air.toml first, legacy .air.conf second. It returns
// the base file name for the Air command line (Air takes the config path
// as its positional argument). A future meta init (Phase H) ships its
// own .air.toml template and this lookup finds it unchanged.
func findAirConfig(root string) (string, error) {
	for _, name := range []string{".air.toml", ".air.conf"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("meta dev: no Air configuration found in %s (expected .air.toml)", root)
}

// checkClientAssets verifies the backend will not instantly die on boot:
// the example server exits fast when dist/client/.vite/manifest.json is
// missing, so fail before launching Air and say how to fix it instead.
func checkClientAssets(root string) error {
	manifest := filepath.Join(root, "dist", "client", ".vite", "manifest.json")
	if _, err := os.Stat(manifest); err != nil {
		return fmt.Errorf(
			"meta dev: client bundle not found (%s missing); run 'make build' or 'npm run build' once before starting the dev server (A04 removes this prerequisite)",
			manifest,
		)
	}
	return nil
}

// runDev implements `meta dev` via the production seams.
func runDev(ctx *Context, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("meta dev: unexpected argument %q (A02 takes no dev arguments)", args[0])
	}
	return runDevWith(ctx, prodDeps)
}

// runDevWith contains the testable core of meta dev: discover the
// project root, check A02 prerequisites, print startup status, then
// supervise Air until it exits or the CLI is interrupted.
//
// Output discipline: startup status goes to stdout; Air's process output
// streams to stderr (it interleaves build diagnostics and server logs);
// errors surface through the Run boundary on stderr.
func runDevWith(c *Context, deps devDeps) error {
	root, err := c.projectRoot()
	if err != nil {
		return err
	}

	airPath, err := deps.lookAir()
	if err != nil {
		return err
	}

	config, err := findAirConfig(root)
	if err != nil {
		return err
	}

	if err := checkClientAssets(root); err != nil {
		return err
	}

	fmt.Fprintf(c.Stdout, "Project: %s\n", root)
	fmt.Fprintf(c.Stdout, "Backend: Air\n")
	fmt.Fprintf(c.Stdout, "Watching: Go sources\n")
	fmt.Fprintf(c.Stdout, "Starting development backend...\n")

	sigCh, stopSignals := deps.signals()
	defer stopSignals()

	stopReconcile, err := startRouteReconciler(root, c.Stderr)
	if err != nil {
		return fmt.Errorf("meta dev: route reconciliation: %w", err)
	}
	defer stopReconcile()

	proc, err := deps.runner.Start(withWorkingDir(context.Background(), root), c.Stderr, airPath, config)
	if err != nil {
		return fmt.Errorf("meta dev: failed to start Air: %w", err)
	}

	waitCh := make(chan error, 1)
	go func() { waitCh <- proc.Wait() }()

	select {
	case waitErr := <-waitCh:
		if waitErr == nil {
			return nil
		}
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			return fmt.Errorf("meta dev: Air exited unexpectedly with code %d", exitErr.ExitCode())
		}
		return fmt.Errorf("meta dev: Air exited unexpectedly: %w", waitErr)

	case sig := <-sigCh:
		fmt.Fprintf(c.Stderr, "meta dev: received %v, stopping development backend\n", sig)
		// Interactive Ctrl+C reaches Air directly (same process group),
		// but when only the meta process is signaled, forward the
		// interrupt so Air tears its child server down itself.
		_ = proc.Signal(sig)
		select {
		case <-waitCh:
			// Air exited on its own within the grace period.
		case <-time.After(deps.grace):
			// Air did not shut down in time; kill and collect its exit.
			_ = proc.Kill()
			<-waitCh
		}
		fmt.Fprintln(c.Stderr, "meta dev: development session ended")
		return nil
	}
}
