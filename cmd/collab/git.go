package main

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// gitChangedPaths derives paths from the exact local commit, so normal CLI
// receipts are not based on a free-form list that may disagree with Git.
func gitChangedPaths(repo, sha string) ([]string, error) {
	check := exec.Command("git", "-C", repo, "cat-file", "-t", sha)
	kind, err := check.Output()
	if err != nil || strings.TrimSpace(string(kind)) != "commit" {
		return nil, errors.New("Git commit object does not exist in the specified repository")
	}
	c := exec.Command("git", "-C", repo, "diff-tree", "--root", "--no-commit-id", "--name-only", "-r", "-z", sha)
	out, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff-tree: %w", err)
	}
	seen := map[string]bool{}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, errors.New("commit has no simple file changes; receipts require changed paths")
	}
	return paths, nil
}
func samePaths(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a = append([]string(nil), a...)
	b = append([]string(nil), b...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
