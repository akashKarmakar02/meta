package cli

import "fmt"

func init() {
	register(&Command{
		Name:  "dev",
		Short: "Start the development server",
		Usage: `Start the development server.

meta dev orchestrates the Go application server, templ regeneration,
and Vite HMR behind a single origin.

Not implemented yet: development orchestration arrives in P2-A02-A05.
In A01 the command validates the project context and reports this
explicitly instead of pretending to run.

Usage:
  meta dev [--dir <path>]

Flags:
  --dir <path>    Project root directory (skips discovery)

Exit status:
  1  when the command runs but is not implemented yet, or the project
     root is missing/invalid.
`,
		Run: runDev,
	})
}

// runDev validates the project context for the development command and
// reports that orchestration is not implemented yet. It deliberately
// fails with a distinctive error rather than exiting successfully while
// doing nothing: a silent no-op success would be indistinguishable from
// a working dev server in scripts and CI.
func runDev(ctx *Context, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("meta dev: unexpected argument %q (A01 takes no dev arguments)", args[0])
	}

	root, err := ctx.projectRoot()
	if err != nil {
		return err
	}

	return &notImplementedError{
		command: "dev",
		detail:  fmt.Sprintf("project root detected at %s; development orchestration is not implemented yet (P2-A02-A05)", root),
	}
}

// notImplementedError marks commands that are recognized but whose
// behavior lands in a later task. The CLI surface exists so scripts and
// docs can stabilize now; running them must fail loudly rather than
// succeed silently doing nothing. The zero Value of this type is NOT an
// error.
type notImplementedError struct {
	command string
	detail  string
}

func (e *notImplementedError) Error() string {
	msg := "meta " + e.command + ": not implemented yet"
	if e.detail != "" {
		return msg + ": " + e.detail
	}
	return msg
}
