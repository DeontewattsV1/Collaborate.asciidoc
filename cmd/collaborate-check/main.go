// Command collaborate-check is the standalone coordination checker shipped by
// the reusable GitHub Action. It depends only on the Go standard library and
// internal/ghcoord, so it builds quickly and reproducibly from the action's
// own pinned source.
//
// Exit codes: 0 PASS, 1 BLOCKED, 2 ERROR, 3 INDETERMINATE.
package main

import (
	"fmt"
	"io"
	"os"

	"collaborate/internal/ghcoord"
)

// version is set at release time with -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "version") {
		fmt.Fprintf(stdout, "collaborate-check %s (report schema %s)\n", version, ghcoord.SchemaVersion)
		return 0
	}
	return ghcoord.RunCLI("collaborate-check", args, stdout, stderr, getenv)
}
