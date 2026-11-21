package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubCheckUsageErrors(t *testing.T) {
	reg := filepath.Join(t.TempDir(), "agents.json")
	if err := os.WriteFile(reg, []byte(`{"version":1,"agents":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(token string, args ...string) (int, string) {
		t.Setenv("GITHUB_TOKEN", token)
		var out, errb bytes.Buffer
		code := runGitHubCheck(args, &out, &errb)
		return code, errb.String()
	}
	if code, msg := run("t", "--repo", "o/r", "--registry", reg); code != 2 || !strings.Contains(msg, "--pr is required") {
		t.Fatalf("missing --pr: code=%d msg=%q", code, msg)
	}
	if code, msg := run("", "--repo", "o/r", "--pr", "1", "--registry", reg); code != 2 || !strings.Contains(msg, "GITHUB_TOKEN") {
		t.Fatalf("missing token: code=%d msg=%q", code, msg)
	}
	if code, msg := run("t", "--repo", "o/r", "--pr", "1", "--registry", filepath.Join(t.TempDir(), "missing.json")); code != 2 || !strings.Contains(msg, "missing.json") {
		t.Fatalf("missing registry: code=%d msg=%q", code, msg)
	}
}
