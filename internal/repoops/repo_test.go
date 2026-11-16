package repoops

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func testRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitTest(t, root, "init", "-b", "main")
	gitTest(t, root, "config", "user.name", "Repository Test")
	gitTest(t, root, "config", "user.email", "repo-test@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("base\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "note.txt")
	gitTest(t, root, "commit", "-m", "base")
	return root
}
func TestInspectCommitDerivesFilesAndPatchHash(t *testing.T) {
	root := testRepo(t)
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("updated\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "a.go"), []byte("package src\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "note.txt", "src/a.go")
	gitTest(t, root, "commit", "-m", "change files")
	sha := gitTest(t, root, "rev-parse", "HEAD")
	receipt, err := InspectCommit(root, sha)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Commit != sha || len(receipt.Tree) != 40 {
		t.Fatalf("receipt identity/tree=%+v", receipt)
	}
	files := map[string]bool{}
	for _, f := range receipt.Files {
		files[f] = true
	}
	if !files["note.txt"] || !files["src/a.go"] || len(files) != 2 {
		t.Fatalf("server-derived files=%v", receipt.Files)
	}
	cmd := exec.Command("git", "show", "--format=", "--no-ext-diff", "--no-renames", sha)
	cmd.Dir = root
	patch, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(patch)
	if receipt.DiffSHA != hex.EncodeToString(sum[:]) {
		t.Fatalf("diff hash=%s want %x", receipt.DiffSHA, sum)
	}
}
func TestRunGatesInDetachedCheckoutAndReportsFailure(t *testing.T) {
	root := testRepo(t)
	sha := gitTest(t, root, "rev-parse", "HEAD")
	results, err := RunGates(root, sha, []Gate{{Name: "present", Command: []string{"sh", "-c", "test -f note.txt"}, TimeoutSeconds: 10}})
	if err != nil || len(results) != 1 || results[0].ExitCode != 0 || results[0].OutputSHA == "" {
		t.Fatalf("RunGates()=(%v,%v)", results, err)
	}
	if _, err = RunGates(root, sha, []Gate{{Name: "reject", Command: []string{"sh", "-c", "exit 7"}, TimeoutSeconds: 10}}); err == nil {
		t.Fatal("failing gate unexpectedly passed")
	}
	if _, err = RunGates(root, sha, nil); err == nil {
		t.Fatal("integration with no configured trusted gates was allowed")
	}
}
func TestMergeQueueCommitAdvancesDedicatedTrunk(t *testing.T) {
	root := testRepo(t)
	trunk := "collab/session/trunk"
	if err := EnsureBranch(root, trunk); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "agent-wt")
	branch := "collab/session/agent-a"
	if err := CreateWorktree(root, branch, wt, trunk); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "note.txt"), []byte("agent change\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTest(t, wt, "add", "note.txt")
	gitTest(t, wt, "commit", "-m", "agent change")
	commit := gitTest(t, wt, "rev-parse", "HEAD")
	baseHead, err := BranchHead(root, trunk)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = MergeQueueCommitWithGates(root, trunk, commit, []Gate{{Name: "reject", Command: []string{"sh", "-c", "exit 7"}}}); !errors.Is(err, ErrGateFailed) {
		t.Fatalf("failed candidate gate error=%v, want ErrGateFailed", err)
	}
	if head, e := BranchHead(root, trunk); e != nil || head != baseHead {
		t.Fatalf("failed gate advanced trunk to %q, err=%v", head, e)
	}
	merged, results, err := MergeQueueCommitWithGates(root, trunk, commit, []Gate{{Name: "merged-content", Command: []string{"sh", "-c", "grep -q 'agent change' note.txt"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ExitCode != 0 {
		t.Fatalf("candidate gate results=%+v", results)
	}
	if merged == commit {
		t.Fatal("expected a dedicated merge commit")
	}
	head, err := BranchHead(root, trunk)
	if err != nil || head != merged {
		t.Fatalf("trunk head=%q err=%v want %q", head, err, merged)
	}
	content := gitTest(t, root, "show", trunk+":note.txt")
	if content != "agent change" {
		t.Fatalf("merged file=%q", content)
	}
	if ok, err := CommitIsBranchHead(root, trunk, commit); err != nil || ok {
		t.Fatalf("branch-head check=(%v,%v), want false for pre-merge agent commit", ok, err)
	}
}
