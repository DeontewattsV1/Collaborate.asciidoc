package main

import (
	"io"
	"os"

	"collaborate/internal/ghcoord"
)

// runGitHubCheck implements `collaborate github-check`. It returns the process
// exit code: 0 pass, 1 blocked, 2 usage/API error, 3 indeterminate (incomplete
// scan). The same logic ships as the standalone cmd/collaborate-check binary
// used by the reusable GitHub Action.
func runGitHubCheck(args []string, stdout, stderr io.Writer) int {
	return ghcoord.RunCLI("collaborate github-check", args, stdout, stderr, os.Getenv)
}
