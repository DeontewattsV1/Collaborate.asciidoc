// Command collaborate-check is the standalone coordination checker shipped by
// the reusable GitHub Action. It depends only on the Go standard library and
// internal/ghcoord, so it builds quickly and reproducibly from the action's
// own pinned source.
//
// Exit codes: 0 PASS, 1 BLOCKED, 2 ERROR, 3 INDETERMINATE.
package main

import (
	"fmt"
	"os"

	"collaborate/internal/ghcoord"
)

// version is set at release time with -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("collaborate-check %s (report schema %s)\n", version, ghcoord.SchemaVersion)
		return
	}
	os.Exit(ghcoord.RunCLI("collaborate-check", os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}
