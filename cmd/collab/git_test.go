package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func TestGitChangedPaths(t *testing.T) {
	dir := t.TempDir()
	gitTest(t, dir, "init", "-q")
	gitTest(t, dir, "config", "user.email", "test@example.invalid")
	gitTest(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTest(t, dir, "add", "a.txt")
	gitTest(t, dir, "commit", "-q", "-m", "initial")
	sha := gitTest(t, dir, "rev-parse", "HEAD")
	paths, err := gitChangedPaths(dir, sha)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "a.txt" {
		t.Fatalf("unexpected paths %v", paths)
	}
	if _, err := gitChangedPaths(dir, strings.Repeat("a", 40)); err == nil {
		t.Fatal("accepted unknown commit")
	}
	if samePaths([]string{"a.txt"}, []string{"b.txt"}) || !samePaths([]string{"x", "y"}, []string{"y", "x"}) {
		t.Fatal("path equality broken")
	}
}
