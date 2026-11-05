// Package cli implements the meta command-line interface: argument
// parsing, command dispatch, and project-root discovery. Process
// orchestration deliberately stays out of this package (A02+) so the CLI
// can be exercised by unit tests without spawning anything.
package cli

import (
	"flag"
	"fmt"
	"io"
)

// Version is the single authoritative CLI version. Development builds
// report "dev"; release builds override it with
//
//	go build -ldflags "-X github.com/akashKarmakar02/meta/internal/cli.Version=<semver>"
var Version = "dev"

// Exit codes are centralized here, at the executable boundary: command
// handlers return errors and never decide process state themselves.
const (
	exitSuccess = 0
	exitFailure = 1
	exitUsage   = 2
)

// Command describes one meta subcommand. Future commands (build, start,
// generate, check, init) register here without touching dispatch logic.
type Command struct {
	// Name is the subcommand as typed by the user.
	Name string
	// Short is the one-line description shown in root help.
	Short string
	// Usage is the full per-command help body (meta help <name>).
	Usage string
	// Run executes the command. It returns an error instead of exiting.
	Run func(ctx *Context, args []string) error
}

// commands is the static registry. Registration happens in init to keep
// this file declarative; nothing mutates it after startup.
var commands = map[string]*Command{}

func register(cmd *Command) { commands[cmd.Name] = cmd }

// Context carries per-invocation state to command handlers. It exists so
// handlers receive their writers and options explicitly instead of
// reaching for globals, keeping them testable in isolation.
type Context struct {
	Stdout io.Writer
	Stderr io.Writer

	// dir is the optional --dir override for project-root discovery.
	dir string
}

// Run parses args, dispatches to a command handler, prints errors to
// stderr, and returns the process exit code. It is the whole CLI surface;
// main supplies arguments and writers and exits with the result.
func Run(args []string, stdout, stderr io.Writer) int {
	ctx := &Context{Stdout: stdout, Stderr: stderr}

	if len(args) == 0 {
		writeRootUsage(ctx.Stdout)
		return exitSuccess
	}

	var showHelp, showVersion bool
	fs := flag.NewFlagSet("meta", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // we render errors ourselves
	fs.StringVar(&ctx.dir, "dir", "", "project root `directory` (skips discovery)")
	fs.BoolVar(&showHelp, "h", false, "show help")
	fs.BoolVar(&showHelp, "help", false, "show help")
	fs.BoolVar(&showVersion, "version", false, "show version")
	if err := fs.Parse(args); err != nil {
		return reportUsage(ctx, err.Error())
	}
	if showVersion {
		// Help and version answer without touching the filesystem, so
		// they work outside any project directory.
		fmt.Fprintf(ctx.Stdout, "meta version %s\n", Version)
		return exitSuccess
	}
	if showHelp {
		writeRootUsage(ctx.Stdout)
		return exitSuccess
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return reportUsage(ctx, "no command given")
	}
	name, cmdArgs := rest[0], rest[1:]

	if name == "help" {
		return runHelpCommand(ctx, cmdArgs)
	}
	cmd, ok := commands[name]
	if !ok {
		return reportUsage(ctx, fmt.Sprintf("unknown command %q", name))
	}
	if err := cmd.Run(ctx, cmdArgs); err != nil {
		return reportError(ctx, err)
	}
	return exitSuccess
}

// runHelpCommand implements "meta help" and "meta help <command>".
func runHelpCommand(ctx *Context, args []string) int {
	switch {
	case len(args) == 0:
		writeRootUsage(ctx.Stdout)
		return exitSuccess
	case len(args) == 1 && args[0] != "help":
		if cmd, ok := commands[args[0]]; ok {
			fmt.Fprint(ctx.Stdout, cmd.Usage)
			return exitSuccess
		}
		return reportUsage(ctx, fmt.Sprintf("no help topic for %q", args[0]))
	default:
		return reportUsage(ctx, "usage: meta help [command]")
	}
}

// reportUsage reports an invocation problem (exit code 2): bad flags,
// unknown commands, wrong arity. It always points at the help text.
func reportUsage(ctx *Context, msg string) int {
	fmt.Fprintf(ctx.Stderr, "meta: %s\n", msg)
	fmt.Fprintln(ctx.Stderr, "Run 'meta --help' for usage.")
	return exitUsage
}

// reportError reports a command failure (exit code 1).
func reportError(ctx *Context, err error) int {
	fmt.Fprintf(ctx.Stderr, "%v\n", err)
	return exitFailure
}

func writeRootUsage(w io.Writer) {
	fmt.Fprint(w, `meta is the command-line tool for meta projects.

Usage:
  meta [flags] <command> [arguments]

Commands:
  dev    Start the development server (orchestrates Go, templ, and Vite)
  help   Show this help, or help for a command

Available in later phases: build, start, generate, check, init.

Flags:
  -h, --help          Show this help
      --version       Show version information
      --dir <path>    Project root directory (skips discovery from the
                      working directory; the directory must be a valid
                      meta project)

Run 'meta help <command>' for command details.
`)
}
