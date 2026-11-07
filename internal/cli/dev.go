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
	"strconv"
	"syscall"
	"time"
)

func init() {
	register(&Command{
		Name:  "dev",
		Short: "Start the development server",
		Usage: `Start the development server.

meta dev discovers the project root, starts Vite and runs the Go backend
under Air (github.com/air-verse/air) for automatic rebuild and restart.

What you get:
  - One public development URL (http://localhost:3000 by default).
  - Go backend hot reload via Air; Svelte HMR via Vite.
  - templ and route metadata regenerate before backend builds.
  - During backend rebuilds the public server answers 503 and recovers
    automatically; it never falls back to Vite.

Usage:
  meta dev [--dir <path>]

Flags:
  --dir <path>    Project root directory (skips discovery)

Environment:
  Development .env files supply application variables to the Go process.
  Shell variables take precedence; restart meta dev after editing files.
  PORT            Public loopback port (default 3000)
  GO_META_VITE_PORT  Vite port override (default 5173)

Exit status:
  0  interrupted development session (Ctrl+C)
  1  missing dependencies or configuration, a port conflict, a process
     failing to start or exiting unexpectedly, or an invalid project root.
`,
		Run: runDev,
	})
}

// airShutdownGrace bounds how long meta waits for Air to tear down after
// an interrupt before killing it outright.
const airShutdownGrace = 5 * time.Second

// devReadyTimeout bounds the startup readiness wait. Cold Go builds of the
// dependency tree can take a while on the first run, so this is generous;
// when it expires the session keeps running (Air recovers when the source
// is fixed) with a diagnostic instead of a hang.
const devReadyTimeout = 120 * time.Second

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

func (p *osProcess) Wait() error                { return p.cmd.Wait() }
func (p *osProcess) Signal(sig os.Signal) error { return p.cmd.Process.Signal(sig) }
func (p *osProcess) Kill() error                { return p.cmd.Process.Kill() }

// execRunner starts real subprocesses.
type execRunner struct{}

// Start launches name in the working directory carried by ctx (see
// withWorkingDir), stdout and stderr both wired to the provided writer so
// Air's interleaved watcher/build/server logs stream through attributed.
// The parent environment is inherited, so PATH/GOPATH/GOFLAGS/PORT flow to
// Air and the server normally.
func (execRunner) Start(ctx context.Context, stdout io.Writer, name string, arg ...string) (process, error) {
	cmd := exec.Command(name, arg...)
	cmd.Dir = workingDirFrom(ctx)
	if env, ok := ctx.Value(ctxKeyEnv).([]string); ok {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stdout
	// Children die with the meta dev process even under SIGKILL, so no
	// orphaned proxy/Air/Vite outlives the session (A06 §10).
	setParentDeathSignal(cmd, syscall.SIGKILL)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &osProcess{cmd}, nil
}

type ctxKey int

const ctxKeyWorkingDir ctxKey = iota
const ctxKeyEnv ctxKey = iota + 1

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
	lookAir    func() (string, error)
	lookNode   func() (string, error)
	runner     runner
	signals    func() (<-chan os.Signal, func())
	grace      time.Duration // override for the shutdown grace period in tests
	startProxy func(string, string, io.Writer) (*devProxy, error)
}

// supervisedChild pairs a process with a close-once completion signal, so
// shutdown code can wait for a child repeatedly without consuming a shared
// channel value (A06 §10: bounded waits, safe fallbacks).
type supervisedChild struct {
	proc process
	done chan struct{}
	err  error // valid after done closes
}

// supervise starts Wait in the background and returns the wrapped child.
// A nil proc (never started) yields an already-finished child.
func supervise(proc process) *supervisedChild {
	ch := &supervisedChild{proc: proc, done: make(chan struct{})}
	if proc == nil {
		close(ch.done)
		return ch
	}
	go func() {
		ch.err = proc.Wait()
		close(ch.done)
	}()
	return ch
}

// stop signals the child and waits a bounded grace period before killing.
func (c *supervisedChild) stop(sig os.Signal, grace time.Duration) {
	if c == nil {
		return
	}
	select {
	case <-c.done:
		return
	default:
	}
	_ = c.proc.Signal(sig)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-c.done:
	case <-timer.C:
		_ = c.proc.Kill()
		<-c.done
	}
}

// exited reports whether the child has terminated.
func (c *supervisedChild) exited() bool {
	if c == nil {
		return true
	}
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// devSession carries the mutable state of one meta dev invocation.
type devSession struct {
	c       *Context
	deps    devDeps
	out     *devOutput // tagged live output stream (stderr)
	log     io.Writer  // underlying writer for out
	root    string
	viteURL string // public-facing URL for humans, "" when no Vite
	vite    *supervisedChild
	air     *supervisedChild
}

// prodDeps is the production wiring. Tests may swap this variable (with
// cleanup) rather than threading a second parameter through runDev.
var prodDeps = devDeps{
	lookAir:    airLookPath,
	lookNode:   nodeLookPath,
	runner:     execRunner{},
	signals:    watchInterrupt,
	grace:      airShutdownGrace,
	startProxy: startDevProxyWithPort,
}

func nodeLookPath() (string, error) {
	path, err := exec.LookPath("node")
	if err != nil {
		return "", fmt.Errorf("meta dev: Node.js was not found.\n\nInstall:\n  https://nodejs.org/ (npm ships with it)\n\nThen install dependencies:\n  npm ci")
	}
	return path, nil
}

func viteCommand(root string, lookNode func() (string, error)) (string, []string, string, error) {
	return viteCommandWithPort(root, lookNode, os.Getenv("GO_META_VITE_PORT"))
}

func viteCommandWithPort(root string, lookNode func() (string, error), port string) (string, []string, string, error) {
	node, err := lookNode()
	if err != nil {
		return "", nil, "", err
	}
	vite := filepath.Join(root, "node_modules", "vite", "bin", "vite.js")
	if _, err := os.Stat(vite); err != nil {
		return "", nil, "", fmt.Errorf("meta dev: Vite dependencies missing (node_modules not installed).\n\nInstall:\n  cd %s\n  npm ci", root)
	}
	if port == "" {
		port = "5173"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", nil, "", fmt.Errorf("meta dev: invalid GO_META_VITE_PORT %q", port)
	}
	origin := "http://127.0.0.1:" + strconv.Itoa(n)
	return node, []string{vite, "--host", "127.0.0.1", "--port", strconv.Itoa(n), "--strictPort", "--base", vitePrefix}, origin, nil
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

Install:
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

// checkClientAssets verifies the backend will not instantly die on boot in
// the no-Node fallback mode: the example server exits fast when
// dist/client/.vite/manifest.json is missing, so fail before launching Air
// and say how to fix it instead.
func checkClientAssets(root string) error {
	manifest := filepath.Join(root, "dist", "client", ".vite", "manifest.json")
	if _, err := os.Stat(manifest); err != nil {
		return fmt.Errorf(
			"meta dev: client bundle not found (%s missing); run 'make build' or 'npm run build' once before starting the dev server, or install Node.js so meta dev can run Vite",
			manifest,
		)
	}
	return nil
}

// runDev implements `meta dev` via the production seams.
func runDev(ctx *Context, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("meta dev: unexpected argument %q (meta dev takes no arguments)", args[0])
	}
	return runDevWith(ctx, prodDeps)
}

// runDevWith contains the testable core of meta dev: discover the
// project root, validate prerequisites, print the startup banner, start
// the proxy, Vite, and Air, wait for genuine readiness, then supervise
// until something exits or the CLI is interrupted.
//
// Output discipline: the banner and the final Ready line go to stdout;
// live subprocess output and meta notices stream to stderr, each line
// attributed to its source ([meta]/[air]/[vite]) by devOutput.
func runDevWith(c *Context, deps devDeps) error {
	root, err := c.projectRoot()
	if err != nil {
		return err
	}
	envValues, err := developmentEnv(root)
	if err != nil {
		return err
	}
	viteEnv, err := fileEnv(root, "development", true)
	if err != nil {
		return err
	}
	s := &devSession{c: c, deps: deps, root: root}
	s.log = c.Stderr
	s.out = newDevOutput(c.Stderr)

	// Air is the core supervisor: verify it (and its config) before the
	// Node+Vite stack so the most fundamental failure surfaces first.
	airPath, err := deps.lookAir()
	if err != nil {
		return err
	}
	config, err := findAirConfig(root)
	if err != nil {
		return err
	}

	var node, origin string
	var viteArgs []string
	if deps.lookNode != nil {
		// Cheap pre-flight validation for the Node+Vite stack; each failure
		// names the dependency and how to fix it (A06 §5).
		if err := checkDevToolchain(root); err != nil {
			return err
		}
		node, viteArgs, origin, err = viteCommandWithPort(root, deps.lookNode, devEnvValue("GO_META_VITE_PORT", envValues))
		if err != nil {
			return err
		}
		if err := checkVitePortFree(portOfOrigin(origin)); err != nil {
			return err
		}
		s.viteURL = origin
	} else if err := checkClientAssets(root); err != nil {
		return err
	}

	publicPort := ""
	if p, perr := publicDevPortValue(devEnvValue("PORT", envValues)); perr == nil {
		publicPort = p
	} else {
		return perr
	}

	fmt.Fprint(c.Stdout, "meta development\n\n")
	fmt.Fprintf(c.Stdout, "Project:    %s\n", root)
	fmt.Fprintf(c.Stdout, "Local:      http://localhost:%s\n", publicPort)
	fmt.Fprint(c.Stdout, "\n")
	fmt.Fprintf(c.Stdout, "Backend:    Air (Go hot rebuild)\n")
	if node != "" {
		fmt.Fprintf(c.Stdout, "Frontend:   Vite at %s (Svelte HMR)\n", origin)
	} else {
		fmt.Fprint(c.Stdout, "Frontend:   built client bundle (no Node.js found)\n")
	}
	fmt.Fprint(c.Stdout, "Generation: automatic (templ + routes)\n\n")
	fmt.Fprint(c.Stdout, "Starting development services...\n")

	sigCh, stopSignals := deps.signals()
	defer stopSignals()

	stopReconcile, err := startRouteReconciler(root, s.out)
	if err != nil {
		return fmt.Errorf("meta dev: route reconciliation: %w", err)
	}
	defer stopReconcile()

	var proxy *devProxy
	var proxyWait <-chan error
	if node != "" && deps.startProxy != nil {
		proxy, err = deps.startProxy(origin, publicPort, s.out)
		if err != nil {
			return err
		}
		defer proxy.Close()
		proxyWait = proxy.done
	}

	if node != "" {
		viteCtx := withWorkingDir(context.Background(), root)
		viteValues := append([]string(nil), viteEnv...)
		if proxy != nil {
			viteValues = append(viteValues, "GO_META_PUBLIC_PORT="+proxy.publicPort)
		}
		if len(viteValues) > 0 {
			viteCtx = context.WithValue(viteCtx, ctxKeyEnv, viteValues)
		}
		viteProc, err := deps.runner.Start(viteCtx, s.out.vite(), node, viteArgs...)
		if err != nil {
			return fmt.Errorf("meta dev: failed to start Vite: %w", err)
		}
		s.vite = supervise(viteProc)
	}

	airCtx := withWorkingDir(context.Background(), root)
	env := append([]string(nil), envValues...)
	env = append(env, "GO_META_DEV=1")
	if origin != "" {
		env = append(env, "GO_META_VITE_ORIGIN="+origin)
		if proxy != nil {
			env = append(env, "GO_META_DEV_SOCKET="+proxy.backendSocket)
		}
	}
	if len(env) != 0 {
		airCtx = context.WithValue(airCtx, ctxKeyEnv, env)
	}
	airProc, err := deps.runner.Start(airCtx, s.out.air(), airPath, config)
	if err != nil {
		s.stopChildren()
		return fmt.Errorf("meta dev: failed to start Air: %w", err)
	}
	s.air = supervise(airProc)

	// Readiness: prove the public proxy and backend answer before claiming
	// the environment is usable. Canceled by Ctrl+C during startup.
	readyCtx, cancelReady := context.WithCancel(context.Background())
	defer cancelReady()
	var readyCh <-chan error
	if proxy != nil {
		ch := make(chan error, 1)
		readyCh = ch
		base := "http://127.0.0.1:" + proxy.publicPort
		go func() { ch <- waitForDevReady(readyCtx, base, devReadyTimeout) }()
	}

	// viteDone stays nil when there is no Vite; selecting on a nil channel
	// blocks forever, which is exactly the right behavior here.
	var viteDone <-chan struct{}
	if s.vite != nil {
		viteDone = s.vite.done
	}

	for {
		select {
		case <-s.air.done:
			waitErr := s.air.err
			s.stopChildren()
			if waitErr == nil {
				if s.vite != nil {
					return errors.New("meta dev: Air exited unexpectedly (its output above usually names the reason; a failed build keeps the previous binary running only while Air itself stays up)")
				}
				return nil
			}
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				return fmt.Errorf("meta dev: Air exited unexpectedly with code %d", exitErr.ExitCode())
			}
			return fmt.Errorf("meta dev: Air exited unexpectedly: %w", waitErr)

		case <-viteDone:
			viteErr := s.vite.err
			s.stopChildren()
			if viteErr == nil {
				return errors.New("meta dev: Vite exited unexpectedly (its output above usually names the reason)")
			}
			return fmt.Errorf("meta dev: Vite exited unexpectedly: %w", viteErr)

		case proxyErr := <-proxyWait:
			s.stopChildren()
			return fmt.Errorf("meta dev: development proxy exited unexpectedly: %w", proxyErr)

		case readyErr := <-readyCh:
			if readyErr == nil || errors.Is(readyErr, context.Canceled) {
				if readyErr == nil {
					fmt.Fprintf(c.Stdout, "Ready: http://localhost:%s\n", publicPort)
				}
				// Canceled means shutdown is in progress via the signal case.
				readyCh = nil
			} else {
				// Bounded wait expired: keep the session alive for recovery
				// (Air rebuilds once the source is fixed), and keep watching
				// so the Ready line still appears once it comes up — but
				// never claim readiness before then (A06 §4).
				s.out.meta("%v; staying up — fix the build above and it will become ready automatically", readyErr)
				ch := make(chan error, 1)
				base := "http://127.0.0.1:" + proxy.publicPort
				go func() { ch <- waitForDevReady(readyCtx, base, devReadyTimeout) }()
				readyCh = ch
			}

		case sig := <-sigCh:
			cancelReady()
			s.out.meta("received %v, stopping development services", sig)
			s.stopChildren()
			s.out.meta("development session ended")
			return nil
		}
	}
}

// stopChildren tears down Vite and Air: both are signaled first so they
// shut down concurrently, then each gets a bounded grace window before a
// forced kill (A06 §10: bounded waits, no orphan children).
func (s *devSession) stopChildren() {
	grace := s.deps.grace
	if grace <= 0 {
		grace = airShutdownGrace
	}
	s.vite.stop(os.Interrupt, grace)
	s.air.stop(os.Interrupt, grace)
}

// portOfOrigin extracts the port from an http://127.0.0.1:<port> origin.
func portOfOrigin(origin string) string {
	if i := len(origin) - 1; i >= 0 {
		for j := i; j >= 0; j-- {
			if origin[j] == ':' {
				return origin[j+1:]
			}
		}
	}
	return origin
}
