package cli

import "fmt"

func init() {
	register(&Command{
		Name:  "devgen",
		Short: "Run development generation (internal; used by meta dev)",
		Usage: `Run the development generation pipeline.

This command is the pre-build generation step meta dev places inside
Air's build command. It is an internal command, not part of the public
CLI surface: run it manually only for debugging generation issues.

It regenerates templ output and route metadata when their inputs changed
since the last successful generation, removes orphaned *_templ.go files,
and exits 0 so the subsequent go build proceeds. On failure it prints the
tool's diagnostics and exits nonzero, leaving previous generated files in
place.

Usage:
  meta devgen [--dir <path>]

Flags:
  --dir <path>    Project root directory (skips discovery)

Exit status:
  1  generation failed (see diagnostics above)
`,
		Run: runDevgen,
	})
}

// runDevgen implements `meta devgen`: project-root discovery plus the
// devgen pipeline. Called from Air's build.cmd with cwd = project root,
// where discovery resolves trivially to the root itself.
func runDevgen(ctx *Context, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("meta devgen: unexpected argument %q", args[0])
	}
	root, err := ctx.projectRoot()
	if err != nil {
		return err
	}
	return devgen(root, ctx.Stdout, ctx.Stderr)
}
