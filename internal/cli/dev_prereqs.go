package cli

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Dependency and configuration validation (A06 §5/§6). Everything here is
// a cheap pre-flight check run before any process is spawned: stat calls,
// one `go version` invocation, one TCP dial. Nothing invokes generators,
// builds, or expensive tools just to prove a dependency exists.

// checkDevToolchain validates everything meta dev's Node+Vite stack needs
// before it may launch processes. Each failure names the missing piece and
// how to fix it (A06 §5). Checks are stat/parse/dial-level only — no
// generators or builds run here.
func checkDevToolchain(root string) error {
	if err := checkGoToolchain(); err != nil {
		return err
	}
	if err := checkTemplTool(root); err != nil {
		return err
	}
	return checkNodeModules(root)
}

// checkGoToolchain verifies the go binary exists and reports a usable
// version. The go directive in go.mod governs toolchain selection, so only
// presence and a parseable version are checked here — not a hard floor.
func checkGoToolchain() error {
	out, err := exec.Command("go", "version").Output()
	if err != nil {
		return fmt.Errorf(`meta dev: Go toolchain was not found.

Install:
  https://go.dev/dl/`)
	}
	v := strings.TrimSpace(string(out))
	if !strings.Contains(v, "go version go") {
		return fmt.Errorf("meta dev: unexpected 'go version' output: %q", v)
	}
	return nil
}

// checkTemplTool verifies the templ generator is reachable through the
// module's tool directive. Air's build chain runs templ generation via
// `go tool templ generate`; without the directive every build fails with
// a confusing "no such tool" diagnostic. Scanning go.mod's text is enough
// (the directive is a line `tool <path>`), and it avoids invoking
// `go tool templ`, which would run the actual generator.
func checkTemplTool(root string) error {
	src, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return fmt.Errorf("meta dev: read %s: %w", filepath.Join(root, "go.mod"), err)
	}
	for _, line := range strings.Split(string(src), "\n") {
		if strings.TrimSpace(line) == "tool "+templToolDirective {
			return nil
		}
	}
	return fmt.Errorf(`meta dev: the Go module does not declare the templ generator.

go.mod needs the tool directive so 'go tool templ generate' resolves:

  go get -tool github.com/a-h/templ/cmd/templ@latest

(templ is required for development generation.)`)
}

// templToolDirective is the go.mod tool directive devgen relies on.
const templToolDirective = "github.com/a-h/templ/cmd/templ"

// checkNodeModules reports whether npm dependencies are installed.
func checkNodeModules(root string) error {
	marker := filepath.Join(root, "node_modules", "vite", "bin", "vite.js")
	if _, err := os.Stat(marker); err != nil {
		return fmt.Errorf(`meta dev: Vite dependencies missing (node_modules not installed).

Install:
  cd %s && npm ci`, root)
	}
	return nil
}

// checkVitePortFree verifies the configured Vite port is bindable before
// spawning anything, so an occupied port fails with a named service
// instead of a Vite stack trace seconds later.
func checkVitePortFree(port string) error {
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		return fmt.Errorf("meta dev: cannot start Vite development server\n\nAddress: 127.0.0.1:%s\nReason:  %v\n\nThe GO_META_VITE_PORT environment variable selects a different port.", port, friendlyDialError(err))
	}
	return ln.Close()
}

// friendlyDialError rewrites low-level listen errors into the shape A06 §6
// asks for; unknown errors pass through unchanged.
func friendlyDialError(err error) string {
	if strings.Contains(err.Error(), "address already in use") {
		return "address already in use"
	}
	if strings.Contains(err.Error(), "permission denied") {
		return "permission denied (privileged port?)"
	}
	return err.Error()
}

// formatPublicPortError renders the public-port failure per A06 §6.
func formatPublicPortError(addr string, err error) error {
	return fmt.Errorf("meta dev: cannot start public development server\n\nAddress: %s\nReason:  %s", addr, friendlyDialError(err))
}

// parsePortString validates and normalizes a numeric port from the
// environment; name distinguishes the variable in errors.
func parsePortString(value, name string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("meta dev: invalid %s %q", name, value)
	}
	return n, nil
}
