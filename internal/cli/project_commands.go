package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const templIgnorePattern = `[/\\]app[/\\].*\[|[/\\]generated[/\\]routepages[/\\]`

func init() {
	for _, command := range []*Command{
		{Name: "setup", Short: "Install project dependencies", Usage: "Install Go tool and frontend dependencies.\n\nUsage:\n  meta setup\n", Run: runSetup},
		{Name: "generate", Short: "Generate templates and routes", Usage: "Generate templ components and route metadata.\n\nUsage:\n  meta generate\n", Run: runGenerate},
		{Name: "build", Short: "Build the production application", Usage: "Generate code and build frontend assets and the Go server.\n\nUsage:\n  meta build\n", Run: runBuild},
	} {
		register(command)
	}
}

func projectCommand(ctx *Context, name string, args []string, fn func(string) error) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(ctx.Stdout, commands[name].Usage)
		return nil
	}
	if len(args) != 0 {
		return fmt.Errorf("meta %s: unexpected argument %q", name, args[0])
	}
	root, err := ctx.projectRoot()
	if err != nil {
		return err
	}
	return fn(root)
}

func runStep(ctx *Context, root, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = root
	cmd.Stdin = os.Stdin
	cmd.Stdout = ctx.Stdout
	cmd.Stderr = ctx.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("meta: %s failed: %w", name, err)
	}
	return nil
}

func runSetup(ctx *Context, args []string) error {
	return projectCommand(ctx, "setup", args, func(root string) error {
		if err := runStep(ctx, root, "go", "list", "-mod=mod", templToolDirective); err != nil {
			return err
		}
		return runStep(ctx, root, "npm", "install")
	})
}

func generateProject(ctx *Context, root string) error {
	if err := runStep(ctx, root, "go", "tool", "templ", "generate", "-ignore-pattern", templIgnorePattern); err != nil {
		return err
	}
	return runStep(ctx, root, "go", "run", "-mod=mod", frameworkModule+"/cmd/routegen")
}

func runGenerate(ctx *Context, args []string) error {
	return projectCommand(ctx, "generate", args, func(root string) error { return generateProject(ctx, root) })
}

func runBuild(ctx *Context, args []string) error {
	return projectCommand(ctx, "build", args, func(root string) error {
		if err := generateProject(ctx, root); err != nil {
			return err
		}
		if err := runStep(ctx, root, "npm", "run", "build"); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
			return fmt.Errorf("meta build: create bin: %w", err)
		}
		return runStep(ctx, root, "go", "build", "-mod=mod", "-o", filepath.Join("bin", "app"), "./cmd/app")
	})
}
