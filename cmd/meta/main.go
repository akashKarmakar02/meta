// Command meta is the meta command-line tool. The executable is a
// thin boundary: it passes arguments and standard streams to cli.Run and
// exits with the returned code. All parsing, dispatch, output, and error
// formatting live in internal/cli so they are testable without spawning
// processes.
package main

import (
	"os"

	"github.com/akashKarmakar02/meta/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
