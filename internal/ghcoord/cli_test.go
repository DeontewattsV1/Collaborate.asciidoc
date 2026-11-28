package ghcoord

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	headC = "cccccccccccccccccccccccccccccccccccccccc"
)

func (f *fakeGitHub) setHead(n int, sha string) {
	pr := f.pulls[n]
	pr["head"] = map[string]string{"ref": pr["head"].(map[string]string)["ref"], "sha": sha}
}

type cliRun struct {
	code   int
	stdout string
	stderr string
	output string
}

func runCLI(t *testing.T, gh *fakeGitHub, env map[string]string, args ...string) cliRun {
	t.Helper()
	dir := t.TempDir()
	reg := filepath.Join(dir, "agents.json")
	if err := os.WriteFile(reg, []byte(testRegistry), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.txt")
	base := []string{"--repo", "o/r", "--registry", reg, "--output", out}
	if gh != nil {
		base = append(base, "--api-url", gh.srv.URL)
	}
	getenv := func(k string) string {
		if v, ok := env[k]; ok {
			return v
		}
		if k == "GITHUB_TOKEN" {
			return "test-token"
		}
		return ""
	}
	var so, se bytes.Buffer
	code := RunCLI("collaborate-check", append(base, args...), &so, &se, getenv)
	o, _ := os.ReadFile(out)
	return cliRun{code: code, stdout: so.String(), stderr: se.String(), output: string(o)}
}

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, s)
	}
	return m
}

func TestCLIBlockedJSONIsDeterministic(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.addPull(24, "claude[bot]", "claude/auth", "2026-11-21T09:00:00Z", "Collaborate-Claim: src/auth/token.ts", "src/auth/session.ts")
	gh.addPull(27, "chatgpt-codex-connector[bot]", "codex/login", "2026-11-21T10:00:00Z", "", "src/ui/login.tsx", "src/auth/session.ts")
	gh.setHead(27, headB)
	first := runCLI(t, gh, nil, "--pr", "27", "--json")
	second := runCLI(t, gh, nil, "--pr", "27", "--json")
	if first.code != ExitBlocked {
		t.Fatalf("code=%d stderr=%s", first.code, first.stderr)
	}
	if first.stdout != second.stdout {
		t.Fatal("JSON output is not deterministic across identical runs")
	}
	m := decode(t, first.stdout)
	if m["schema_version"] != SchemaVersion || m["outcome"] != "BLOCKED" || m["head_sha"] != headB || m["pr_state"] != "open" {
		t.Fatalf("report header=%v", m)
	}
	files := m["pr"].(map[string]any)["files"].([]any)
	if files[0] != "src/auth/session.ts" {
		t.Fatalf("files not sorted: %v", files)
	}
	for _, k := range []string{"notes", "reconciled", "reviews"} {
		if _, ok := m[k].([]any); !ok {
			t.Fatalf("%s must be an array, got %#v", k, m[k])
		}
	}
	if !strings.Contains(first.output, "outcome=BLOCKED\nexit_code=1\nblocking_conflicts=1\nhead_sha="+headB) {
		t.Fatalf("outputs=%q", first.output)
	}
}

func TestCLIIncompleteScanIsIndeterminateNotPass(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.addPull(1, "a", "x", "2026-11-21T09:00:00Z", "", "a.txt")
	gh.addPull(2, "b", "y", "2026-11-21T10:00:00Z", "", "b.txt")
	gh.addPull(3, "c", "z", "2026-11-21T11:00:00Z", "", "c.txt")
	r := runCLI(t, gh, nil, "--pr", "3", "--max-open", "1", "--json")
	if r.code != ExitIndeterminate {
		t.Fatalf("code=%d, want %d; stderr=%s", r.code, ExitIndeterminate, r.stderr)
	}
	m := decode(t, r.stdout)
	cov := m["coverage"].(map[string]any)
	if m["outcome"] != "INDETERMINATE" || cov["complete"] != false || cov["other_open_compared"] != float64(1) || cov["other_open_listed"] != float64(2) {
		t.Fatalf("outcome=%v coverage=%v", m["outcome"], cov)
	}
	if r := runCLI(t, gh, nil, "--pr", "3", "--max-open", "1", "--allow-indeterminate"); r.code != ExitPass || !strings.Contains(r.stdout, "not a verified pass") {
		t.Fatalf("allow-indeterminate: code=%d out=%s", r.code, r.stdout)
	}
	if r := runCLI(t, gh, nil, "--pr", "3"); r.code != ExitPass || !strings.Contains(r.stdout, "2/2 other open PRs compared (complete)") {
		t.Fatalf("complete scan: code=%d out=%s", r.code, r.stdout)
	}
}

func TestCLIStaleEventAndMovingHead(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.addPull(4, "a", "x", "2026-11-21T09:00:00Z", "", "a.txt")
	gh.setHead(4, headA)
	if r := runCLI(t, gh, nil, "--pr", "4", "--head-sha", headA); r.code != ExitPass {
		t.Fatalf("matching head: code=%d %s", r.code, r.stderr)
	}
	r := runCLI(t, gh, nil, "--pr", "4", "--head-sha", headC, "--json")
	if r.code != ExitIndeterminate || !strings.Contains(r.stdout, "stale_event_head") {
		t.Fatalf("stale head: code=%d %s", r.code, r.stdout)
	}
	gh.flipHead[4] = headB
	gh.pullGets[4] = 0
	r = runCLI(t, gh, nil, "--pr", "4", "--json")
	if r.code != ExitIndeterminate || !strings.Contains(r.stdout, "pr_changed_during_scan") {
		t.Fatalf("moving head: code=%d %s", r.code, r.stdout)
	}
}

func TestCLIClosedPRReleasesLeaseAndReconciles(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.addPull(24, "claude[bot]", "claude/auth", "2026-11-21T09:00:00Z", "", "src/auth/session.ts")
	gh.addPull(27, "chatgpt-codex-connector[bot]", "codex/login", "2026-11-21T10:00:00Z", "", "src/auth/session.ts")
	gh.setHead(24, headA)
	gh.setHead(27, headB)

	// While #24 is open, #27 is blocked and its status says so.
	if r := runCLI(t, gh, nil, "--pr", "27", "--set-status", "--comment"); r.code != ExitBlocked {
		t.Fatalf("open: code=%d %s", r.code, r.stderr)
	}
	if gh.statuses[headB]["state"] != "failure" || gh.statuses[headB]["context"] != StatusContext {
		t.Fatalf("status=%v", gh.statuses[headB])
	}

	// #24 merges: its own run passes and reconciliation clears #27.
	gh.pulls[24]["state"] = "closed"
	gh.pulls[24]["merged"] = true
	r := runCLI(t, gh, map[string]string{"GITHUB_SERVER_URL": "https://github.com", "GITHUB_REPOSITORY": "o/r", "GITHUB_RUN_ID": "42"},
		"--pr", "24", "--reconcile", "--set-status", "--comment", "--json")
	if r.code != ExitPass {
		t.Fatalf("merged: code=%d %s", r.code, r.stderr)
	}
	m := decode(t, r.stdout)
	if m["pr_state"] != "merged" || m["lease_held"] != false || m["outcome"] != "PASS" {
		t.Fatalf("merged report=%v", m)
	}
	rec := m["reconciled"].([]any)
	if len(rec) != 1 || rec[0].(map[string]any)["number"] != float64(27) || rec[0].(map[string]any)["outcome"] != "PASS" {
		t.Fatalf("reconciled=%v", rec)
	}
	if gh.statuses[headB]["state"] != "success" || gh.statuses[headB]["target_url"] != "https://github.com/o/r/actions/runs/42" {
		t.Fatalf("#27 status not refreshed: %v", gh.statuses[headB])
	}
	if _, ok := gh.statuses[headA]; ok {
		t.Fatal("a merged PR must not get a new status")
	}
	var c27 string
	for _, c := range gh.comments {
		if c["issue"] == 27 {
			c27 = c["body"].(string)
		}
	}
	if !strings.Contains(c27, "Collaborate coordination: PASS") {
		t.Fatalf("#27 comment not refreshed:\n%s", c27)
	}
}

func TestCLIPermissionsDegradeGracefully(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.addPull(1, "a", "x", "2026-11-21T09:00:00Z", "", "a.txt")
	gh.setHead(1, headA)
	gh.readOnly, gh.noStatus = true, true
	r := runCLI(t, gh, nil, "--pr", "1", "--comment", "--set-status")
	if r.code != ExitPass || !strings.Contains(r.stderr, "::warning::coordination comment") || !strings.Contains(r.stderr, "::warning::commit status") {
		t.Fatalf("code=%d stderr=%s", r.code, r.stderr)
	}
}

func TestCLIRegistryFromAPI(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.addPull(1, "x", "claude/a", "2026-11-21T09:00:00Z", "", "a.txt")
	gh.contents["main:.github/collaborate-agents.json"] = testRegistry
	r := runCLI(t, gh, nil, "--pr", "1", "--registry", ".github/collaborate-agents.json", "--registry-ref", "main", "--json")
	if r.code != ExitPass || !strings.Contains(r.stdout, `"agent_id": "claude"`) {
		t.Fatalf("code=%d out=%s err=%s", r.code, r.stdout, r.stderr)
	}
	r = runCLI(t, gh, nil, "--pr", "1", "--registry", ".github/missing.json", "--registry-ref", "main")
	if r.code != ExitError || !strings.Contains(r.stderr, "not found") {
		t.Fatalf("missing required registry: code=%d %s", r.code, r.stderr)
	}
	r = runCLI(t, gh, nil, "--pr", "1", "--registry", ".github/missing.json", "--registry-ref", "main", "--registry-optional")
	if r.code != ExitPass || !strings.Contains(r.stdout, "agents are unattributed") {
		t.Fatalf("optional registry: code=%d %s", r.code, r.stdout)
	}
}

func TestCLIInputValidation(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{"missing pr", nil, nil, "--pr is required"},
		{"negative pr", nil, []string{"--pr", "-3"}, "--pr is required"},
		{"bad repo", nil, []string{"--pr", "1", "--repo", "o/r/x"}, "OWNER/NAME"},
		{"dot repo", nil, []string{"--pr", "1", "--repo", "o/.."}, "OWNER/NAME"},
		{"max-open range", nil, []string{"--pr", "1", "--max-open", "0"}, "--max-open"},
		{"timeout range", nil, []string{"--pr", "1", "--timeout", "1ms"}, "--timeout"},
		{"bad head sha", nil, []string{"--pr", "1", "--head-sha", "abc"}, "--head-sha"},
		{"bad ref", nil, []string{"--pr", "1", "--registry-ref", "../main"}, "--registry-ref"},
		{"bad comment author", nil, []string{"--pr", "1", "--comment-author", "a b"}, "--comment-author"},
		{"plain http api", nil, []string{"--pr", "1", "--api-url", "http://example.com"}, "https"},
		{"api with creds", nil, []string{"--pr", "1", "--api-url", "https://u:p@api.github.com"}, "credentials"},
		{"positional", nil, []string{"--pr", "1", "extra"}, "unexpected argument"},
		{"no token", map[string]string{"GITHUB_TOKEN": ""}, []string{"--pr", "1"}, "GITHUB_TOKEN"},
		{"token newline", map[string]string{"GITHUB_TOKEN": "a\nb"}, []string{"--pr", "1"}, "whitespace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := runCLI(t, nil, tc.env, append([]string{"--json"}, tc.args...)...)
			if r.code != ExitError || !strings.Contains(r.stderr, tc.want) {
				t.Fatalf("code=%d stderr=%q, want exit 2 mentioning %q", r.code, r.stderr, tc.want)
			}
			m := decode(t, r.stdout)
			if m["outcome"] != "ERROR" || m["schema_version"] != SchemaVersion {
				t.Fatalf("error document=%v", m)
			}
			if !strings.Contains(r.output, "outcome=ERROR\nexit_code=2") {
				t.Fatalf("outputs=%q", r.output)
			}
		})
	}
}

func TestExitCodesAndStatusStates(t *testing.T) {
	for _, tc := range []struct {
		o     Outcome
		allow bool
		code  int
		state string
	}{
		{OutcomePass, false, 0, "success"},
		{OutcomeBlocked, false, 1, "failure"},
		{OutcomeError, false, 2, "error"},
		{OutcomeIndeterminate, false, 3, "failure"},
		{OutcomeIndeterminate, true, 0, "success"},
	} {
		if got := ExitCode(tc.o, tc.allow); got != tc.code {
			t.Errorf("ExitCode(%s,%v)=%d want %d", tc.o, tc.allow, got, tc.code)
		}
		if got, _ := StatusState(tc.o, tc.allow); got != tc.state {
			t.Errorf("StatusState(%s,%v)=%s want %s", tc.o, tc.allow, got, tc.state)
		}
	}
}
