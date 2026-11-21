package ghcoord

import (
	"os"
	"strings"
	"testing"
	"time"
)

const testRegistry = `{
  "version": 1,
  "humans": ["DeontewattsV1"],
  "agents": [
    {"id": "claude", "platform": "Anthropic Claude", "logins": ["claude[bot]"], "branch_prefixes": ["claude/"], "trailer_emails": ["noreply@anthropic.com"]},
    {"id": "copilot", "platform": "GitHub Copilot", "logins": ["Copilot", "copilot-swe-agent[bot]"], "branch_prefixes": ["copilot/"]},
    {"id": "codex", "platform": "OpenAI Codex", "logins": ["chatgpt-codex-connector[bot]"], "branch_prefixes": ["codex/"]},
    {"id": "cubic", "platform": "cubic", "logins": ["cubic-dev-ai[bot]"]},
    {"id": "dependabot", "platform": "Dependabot", "kind": "automation", "logins": ["dependabot[bot]"], "branch_prefixes": ["dependabot/"]}
  ]
}`

func mustRegistry(t *testing.T, s string) *Registry {
	t.Helper()
	r, err := ParseRegistry([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCheckedInRegistryIsValid(t *testing.T) {
	data, err := os.ReadFile("../../.github/collaborate-agents.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRegistry(data); err != nil {
		t.Fatalf("checked-in registry invalid: %v", err)
	}
}

func TestRegistryValidation(t *testing.T) {
	for name, body := range map[string]string{
		"version":          `{"version":2,"agents":[]}`,
		"unknown field":    `{"version":1,"agents":[],"bypass":true}`,
		"bad id":           `{"version":1,"agents":[{"id":"Bad ID","logins":["x"]}]}`,
		"duplicate id":     `{"version":1,"agents":[{"id":"a","logins":["x"]},{"id":"a","logins":["y"]}]}`,
		"duplicate login":  `{"version":1,"agents":[{"id":"a","logins":["X[bot]"]},{"id":"b","logins":["x[bot]"]}]}`,
		"human collision":  `{"version":1,"humans":["alice"],"agents":[{"id":"a","logins":["Alice"]}]}`,
		"prefix slash":     `{"version":1,"agents":[{"id":"a","branch_prefixes":["claude"]}]}`,
		"duplicate prefix": `{"version":1,"agents":[{"id":"a","branch_prefixes":["x/"]},{"id":"b","branch_prefixes":["x/"]}]}`,
		"no identity":      `{"version":1,"agents":[{"id":"a"}]}`,
		"bad kind":         `{"version":1,"agents":[{"id":"a","kind":"robot","logins":["x"]}]}`,
		"bad email":        `{"version":1,"agents":[{"id":"a","trailer_emails":["nobody"]}]}`,
	} {
		if _, err := ParseRegistry([]byte(body)); err == nil {
			t.Errorf("%s: registry accepted, want error", name)
		}
	}
}

func TestAttributionPrecedence(t *testing.T) {
	r := mustRegistry(t, testRegistry)
	cases := []struct {
		login, branch string
		trailers      []string
		id, source    string
		verified      bool
	}{
		{"claude[bot]", "feature/x", nil, "claude", "login", true},
		{"COPILOT", "main-fix", nil, "copilot", "login", true}, // logins are case-insensitive
		{"DeontewattsV1", "claude/brave-hawking", nil, "claude", "branch", false},
		{"DeontewattsV1", "codex/task-1", []string{"noreply@anthropic.com"}, "codex", "branch", false},
		{"DeontewattsV1", "feature/y", []string{"NoReply@Anthropic.com"}, "claude", "trailer", false},
		{"DeontewattsV1", "feature/z", nil, "DeontewattsV1", "human", true},
		{"stranger", "feature/z", nil, "", "unattributed", false},
		// A registered bot login wins over a misleading branch prefix.
		{"dependabot[bot]", "claude/spoof", nil, "dependabot", "login", true},
	}
	for _, c := range cases {
		a := r.Attribute(c.login, c.branch, c.trailers)
		if a.AgentID != c.id || a.Source != c.source || a.Verified != c.verified {
			t.Errorf("Attribute(%q,%q,%v) = %+v, want id=%q source=%q verified=%v", c.login, c.branch, c.trailers, a, c.id, c.source, c.verified)
		}
	}
}

func TestTrailerEmails(t *testing.T) {
	got := TrailerEmails([]string{
		"Fix\n\nCo-Authored-By: Claude <noreply@anthropic.com>\nco-authored-by: Bob <BOB@example.com>",
		"Other\n\nCo-authored-by: Claude <noreply@anthropic.com>",
		"Mentions Co-Authored-By: inline <not@trailer.com> mid-line",
	})
	want := []string{"bob@example.com", "noreply@anthropic.com"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("TrailerEmails=%v, want %v", got, want)
	}
}

func TestParseClaims(t *testing.T) {
	claims, invalid := ParseClaims("Intent\n\nCollaborate-Claim: internal/broker/\ncollaborate-claim: `docs/MCP.md`\nCollaborate-Claim: ../etc\nCollaborate-Claim: /abs\nCollaborate-Claim: internal/broker")
	if strings.Join(claims, ",") != "docs/MCP.md,internal/broker" {
		t.Fatalf("claims=%v", claims)
	}
	if len(invalid) != 2 {
		t.Fatalf("invalid=%v, want 2 entries", invalid)
	}
}

func TestOverlapsIsHierarchicalNotPrefix(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"a/b.go", "a/b.go", true},
		{"internal/broker", "internal/broker/server.go", true},
		{"internal/broker/server.go", "internal/broker", true},
		{"internal/broker", "internal/brokerage/x.go", false},
		{"README.md", "docs/README.md", false},
	} {
		if got := overlaps(c.a, c.b); got != c.want {
			t.Errorf("overlaps(%q,%q)=%v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func at(min int) time.Time { return time.Date(2026, 11, 21, 10, min, 0, 0, time.UTC) }

func TestAnalyzeConflictsEarliestHolds(t *testing.T) {
	r := mustRegistry(t, testRegistry)
	current := PR{Number: 20, BaseRef: "main", CreatedAt: at(30), Files: []string{"internal/broker/server.go", "README.md"},
		Attribution: r.Attribute("DeontewattsV1", "claude/x", nil)}
	older := PR{Number: 18, Title: "Copilot refactor", BaseRef: "main", CreatedAt: at(10), Claims: []string{"internal/broker"},
		Attribution: r.Attribute("Copilot", "copilot/y", nil)}
	newer := PR{Number: 25, BaseRef: "main", CreatedAt: at(50), Files: []string{"README.md"},
		Attribution: r.Attribute("chatgpt-codex-connector[bot]", "codex/z", nil)}
	otherBase := PR{Number: 21, BaseRef: "release", CreatedAt: at(1), Files: []string{"README.md"}}
	disjoint := PR{Number: 22, BaseRef: "main", CreatedAt: at(2), Files: []string{"docs/MCP.md"}}

	rep := Analyze(r, current, []PR{newer, older, otherBase, disjoint, current}, nil)
	if rep.BlockingConflictCount != 1 || !rep.Failed() {
		t.Fatalf("blocking=%d failed=%v, want 1 blocking conflict", rep.BlockingConflictCount, rep.Failed())
	}
	if len(rep.Conflicts) != 2 {
		t.Fatalf("conflicts=%+v, want 2 (older blocks, newer yields)", rep.Conflicts)
	}
	if c := rep.Conflicts[0]; c.Other.Number != 18 || !c.Blocking || c.Holder != 18 || strings.Join(c.Paths, ",") != "internal/broker/server.go" {
		t.Fatalf("older conflict=%+v", c)
	}
	if c := rep.Conflicts[1]; c.Other.Number != 25 || c.Blocking || c.Holder != 20 {
		t.Fatalf("newer conflict=%+v", c)
	}
	if len(rep.Open) != 4 {
		t.Fatalf("open=%d, want 4 other PRs listed as shared context", len(rep.Open))
	}
	md := rep.Markdown()
	for _, want := range []string{CommentMarker, "❌ 1 lease conflict", "#18 Copilot refactor", "`copilot`", "from branch prefix `claude/` (self-reported)", "Other open agent work"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestAnalyzeIndependentReview(t *testing.T) {
	r := mustRegistry(t, testRegistry)
	r.EnforceIndependentReview = true
	current := PR{Number: 7, Author: "DeontewattsV1", Branch: "claude/x", HeadSHA: "head2",
		Attribution: r.Attribute("DeontewattsV1", "claude/x", nil)}
	check := func(name string, reviews []Review, wantApproved, wantFailed bool) {
		t.Helper()
		rep := Analyze(r, current, nil, reviews)
		if rep.IndependentApproval != wantApproved || rep.Failed() != wantFailed {
			t.Errorf("%s: approved=%v failed=%v, want %v/%v (reviews=%+v)", name, rep.IndependentApproval, rep.Failed(), wantApproved, wantFailed, rep.Reviews)
		}
	}
	check("no reviews", nil, false, true)
	check("same agent approves", []Review{{Reviewer: "claude[bot]", State: "APPROVED", CommitID: "head2", SubmittedAt: at(1)}}, false, true)
	check("author approves", []Review{{Reviewer: "deontewattsv1", State: "APPROVED", CommitID: "head2", SubmittedAt: at(1)}}, false, true)
	check("stale approval", []Review{{Reviewer: "cubic-dev-ai[bot]", State: "APPROVED", CommitID: "head1", SubmittedAt: at(1)}}, false, true)
	check("other agent approves head", []Review{{Reviewer: "cubic-dev-ai[bot]", State: "APPROVED", CommitID: "head2", SubmittedAt: at(1)}}, true, false)
	check("unregistered human approves", []Review{{Reviewer: "colleague", State: "APPROVED", CommitID: "head2", SubmittedAt: at(1)}}, true, false)
	check("comment after approval keeps approval", []Review{
		{Reviewer: "cubic-dev-ai[bot]", State: "APPROVED", CommitID: "head2", SubmittedAt: at(1)},
		{Reviewer: "cubic-dev-ai[bot]", State: "COMMENTED", CommitID: "head2", SubmittedAt: at(2)},
	}, true, false)
	check("dismissed approval", []Review{
		{Reviewer: "cubic-dev-ai[bot]", State: "APPROVED", CommitID: "head2", SubmittedAt: at(1)},
		{Reviewer: "cubic-dev-ai[bot]", State: "DISMISSED", CommitID: "head2", SubmittedAt: at(2)},
	}, false, true)
	check("changes requested blocks", []Review{
		{Reviewer: "cubic-dev-ai[bot]", State: "APPROVED", CommitID: "head2", SubmittedAt: at(1)},
		{Reviewer: "Copilot", State: "CHANGES_REQUESTED", CommitID: "head2", SubmittedAt: at(2)},
	}, true, true)

	r.EnforceIndependentReview = false
	if rep := Analyze(r, current, nil, nil); rep.Failed() {
		t.Fatal("review is advisory when enforce_independent_review is false")
	}
}

func TestMarkdownEscapesTitles(t *testing.T) {
	r := mustRegistry(t, testRegistry)
	cur := PR{Number: 1, BaseRef: "main", CreatedAt: at(5), Files: []string{"a"}}
	other := PR{Number: 2, Title: "evil | `x`\n## injected", BaseRef: "main", CreatedAt: at(1), Files: []string{"a"}}
	md := Analyze(r, cur, []PR{other}, nil).Markdown()
	if strings.Contains(md, "\n## injected") || strings.Contains(md, "evil | ") {
		t.Fatalf("title not escaped:\n%s", md)
	}
}
