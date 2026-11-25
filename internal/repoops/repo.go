package repoops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var fullObjectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

var ErrBranchMoved = errors.New("integration branch moved concurrently; retry queue item")
var ErrGateFailed = errors.New("trusted integration gate failed")

type Receipt struct {
	Commit  string   `json:"commit"`
	Tree    string   `json:"tree"`
	DiffSHA string   `json:"diff_sha"`
	Files   []string `json:"files"`
}
type Gate struct {
	Name           string   `json:"name"`
	Command        []string `json:"command"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}
type GateResult struct {
	Name       string `json:"name"`
	ExitCode   int    `json:"exit_code"`
	OutputSHA  string `json:"output_sha"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// safeGitArg rejects empty values and option-like arguments so user-controlled
// strings cannot be interpreted as git flags (for example --upload-pack).
func safeGitArg(s string) (string, error) {
	if s == "" || strings.HasPrefix(s, "-") {
		return "", fmt.Errorf("invalid git argument %q", s)
	}
	return s, nil
}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if _, err := safeGitArg(dir); err != nil {
		return nil, fmt.Errorf("invalid git directory: %w", err)
	}
	c := exec.CommandContext(ctx, "git", args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return out, nil
}

// gitVerified appends user-controlled values only after safeGitArg rejects option-like strings.
// flags may include a trailing "--" when that is end-of-options for the subcommand; do not
// put revisions after "--" for show, diff-tree, or rev-parse, which treat "--" as a pathspec split.
func gitVerified(ctx context.Context, dir string, flags []string, values ...string) ([]byte, error) {
	args := append([]string{}, flags...)
	for _, v := range values {
		s, err := safeGitArg(v)
		if err != nil {
			return nil, err
		}
		args = append(args, s)
	}
	return git(ctx, dir, args...)
}

// InspectCommit derives receipt metadata from Git objects in the trusted checkout.
// Client-submitted file lists and patch hashes are never used as authority.
func InspectCommit(root, commit string) (Receipt, error) {
	sha := strings.ToLower(strings.TrimSpace(commit))
	if !fullObjectID.MatchString(sha) {
		return Receipt{}, fmt.Errorf("commit must be a full Git object ID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resolved, err := gitVerified(ctx, root, []string{"rev-parse", "--verify", "--end-of-options"}, sha+"^{commit}")
	if err != nil {
		return Receipt{}, err
	}
	sha = strings.TrimSpace(string(resolved))
	tree, err := gitVerified(ctx, root, []string{"show", "-s", "--format=%T"}, sha)
	if err != nil {
		return Receipt{}, err
	}
	patch, err := gitVerified(ctx, root, []string{"show", "--format=", "--no-ext-diff", "--no-renames"}, sha)
	if err != nil {
		return Receipt{}, err
	}
	sum := sha256.Sum256(patch)
	filesRaw, err := gitVerified(ctx, root, []string{"diff-tree", "--root", "--no-commit-id", "--name-only", "-r", "-z", "--no-renames"}, sha)
	if err != nil {
		return Receipt{}, err
	}
	files := make([]string, 0)
	for _, f := range strings.Split(string(filesRaw), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return Receipt{Commit: sha, Tree: strings.TrimSpace(string(tree)), DiffSHA: hex.EncodeToString(sum[:]), Files: files}, nil
}

// RunGates executes only operator-supplied argv arrays, without a shell, against a detached clean worktree.
// Gate configuration is trusted server configuration; do not load it from an unreviewed agent branch.
func RunGates(root, commit string, gates []Gate) ([]GateResult, error) {
	if len(gates) == 0 {
		return nil, errors.New("no trusted gates configured; refusing integration")
	}
	tmp, err := os.MkdirTemp("", "collaborate-gates-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err = gitVerified(ctx, root, []string{"worktree", "add", "--detach", "--quiet", "--"}, tmp, commit); err != nil {
		return nil, err
	}
	defer func() {
		clean, c := context.WithTimeout(context.Background(), 15*time.Second)
		defer c()
		_, _ = gitVerified(clean, root, []string{"worktree", "remove", "--force", "--"}, tmp)
	}()
	results := make([]GateResult, 0, len(gates))
	for _, g := range gates {
		if strings.TrimSpace(g.Name) == "" || len(g.Command) == 0 || strings.TrimSpace(g.Command[0]) == "" {
			return results, fmt.Errorf("invalid gate configuration: name and command are required")
		}
		timeout := time.Duration(g.TimeoutSeconds) * time.Second
		if timeout <= 0 || timeout > 10*time.Minute {
			timeout = 2 * time.Minute
		}
		gctx, stop := context.WithTimeout(context.Background(), timeout)
		start := time.Now()
		cmd := exec.CommandContext(gctx, g.Command[0], g.Command[1:]...)
		cmd.Dir = tmp
		home := filepath.Join(tmp, ".collab-gate-home")
		if err := os.MkdirAll(home, 0700); err != nil {
			stop()
			return results, err
		}
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TMPDIR=" + home, "CI=1", "COLLAB_GATE=1"}
		out, runErr := cmd.CombinedOutput()
		stop()
		sum := sha256.Sum256(out)
		r := GateResult{Name: g.Name, ExitCode: 0, OutputSHA: hex.EncodeToString(sum[:]), DurationMS: time.Since(start).Milliseconds()}
		if runErr != nil {
			r.ExitCode = 1
			if ee, ok := runErr.(*exec.ExitError); ok {
				r.ExitCode = ee.ExitCode()
			}
			r.Error = runErr.Error()
		}
		results = append(results, r)
		if runErr != nil {
			return results, fmt.Errorf("%w: %s (exit %d): %v", ErrGateFailed, g.Name, r.ExitCode, runErr)
		}
	}
	return results, nil
}

func ValidBranch(s string) bool {
	return s != "" && !strings.HasPrefix(s, "-") && !strings.Contains(s, "..") && !strings.ContainsAny(s, " ~^:?*[\\")
}
func EnsureBranch(root, branch string) error {
	if !ValidBranch(branch) {
		return fmt.Errorf("invalid branch name %q", branch)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := gitVerified(ctx, root, []string{"show-ref", "--verify", "--quiet", "--"}, "refs/heads/"+branch); err == nil {
		return nil
	}
	head, err := git(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	_, err = gitVerified(ctx, root, []string{"branch", "--"}, branch, strings.TrimSpace(string(head)))
	return err
}
func BranchHead(root, branch string) (string, error) {
	if !ValidBranch(branch) {
		return "", fmt.Errorf("invalid branch name %q", branch)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	b, err := gitVerified(ctx, root, []string{"rev-parse", "--verify", "--end-of-options"}, "refs/heads/"+branch+"^{commit}")
	return strings.TrimSpace(string(b)), err
}
func CommitIsBranchHead(root, branch, commit string) (bool, error) {
	head, err := BranchHead(root, branch)
	if err != nil {
		return false, err
	}
	return head == commit, nil
}
func CreateWorktree(root, branch, worktree, base string) error {
	if !ValidBranch(branch) {
		return fmt.Errorf("invalid branch name %q", branch)
	}
	if !ValidBranch(base) {
		return fmt.Errorf("invalid worktree base branch %q", base)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := os.MkdirAll(filepath.Dir(worktree), 0700); err != nil {
		return err
	}
	branch, err := safeGitArg(branch)
	if err != nil {
		return err
	}
	_, err = gitVerified(ctx, root, []string{"worktree", "add", "-b", branch, "--"}, worktree, base)
	return err
}

// MergeQueueCommit merges without running gates. It is retained for repository
// tooling/tests; the broker should use MergeQueueCommitWithGates.
func MergeQueueCommit(root, trunk, commit string) (string, error) {
	head, _, err := mergeQueueCommit(root, trunk, commit, nil)
	return head, err
}

// MergeQueueCommitWithGates creates a merge candidate from the current trunk,
// tests the resulting merge tree in a detached worktree, and advances trunk only
// if it has not moved since candidate construction.
func MergeQueueCommitWithGates(root, trunk, commit string, gates []Gate) (string, []GateResult, error) {
	if len(gates) == 0 {
		return "", nil, errors.New("no trusted gates configured; refusing integration")
	}
	return mergeQueueCommit(root, trunk, commit, gates)
}

func mergeQueueCommit(root, trunk, commit string, gates []Gate) (string, []GateResult, error) {
	if !ValidBranch(trunk) {
		return "", nil, errors.New("invalid integration branch")
	}
	if _, err := InspectCommit(root, commit); err != nil {
		return "", nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	old, err := gitVerified(ctx, root, []string{"rev-parse"}, "refs/heads/"+trunk)
	if err != nil {
		return "", nil, err
	}
	oldSHA := strings.TrimSpace(string(old))
	if _, err = gitVerified(ctx, root, []string{"merge-base", "--is-ancestor", "--"}, commit, oldSHA); err == nil {
		var results []GateResult
		if len(gates) > 0 {
			results, err = RunGates(root, oldSHA, gates)
			if err != nil {
				return "", results, err
			}
		}
		return oldSHA, results, nil
	}
	tmp, err := os.MkdirTemp("", "collaborate-merge-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(tmp)
	if _, err = gitVerified(ctx, root, []string{"worktree", "add", "--detach", "--quiet", "--"}, tmp, oldSHA); err != nil {
		return "", nil, err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = gitVerified(c, root, []string{"worktree", "remove", "--force", "--"}, tmp)
	}()
	if _, err = gitVerified(ctx, tmp, []string{"-c", "user.name=Collaborate Integrator", "-c", "user.email=collaborate@localhost", "merge", "--no-edit", "--no-ff", "--"}, commit); err != nil {
		return "", nil, err
	}
	head, err := git(ctx, tmp, "rev-parse", "HEAD")
	if err != nil {
		return "", nil, err
	}
	newSHA := strings.TrimSpace(string(head))
	var results []GateResult
	if len(gates) > 0 {
		results, err = RunGates(root, newSHA, gates)
		if err != nil {
			return "", results, err
		}
	}
	if _, err = gitVerified(ctx, root, []string{"update-ref", "--"}, "refs/heads/"+trunk, newSHA, oldSHA); err != nil {
		return "", results, fmt.Errorf("%w: %v", ErrBranchMoved, err)
	}
	return newSHA, results, nil
}
