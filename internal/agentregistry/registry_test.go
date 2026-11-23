package agentregistry

import (
	"strings"
	"testing"
)

func validRegistryJSON() string {
	return `{"$schema":"./agent-registry.schema.json","schema_version":1,"agents":[{"agent_id":"claude","display_name":"Claude","status":"active","github_identities":[{"account_type":"user","login":"alice","github_user_id":10}],"branch_prefixes":["claude/"],"allowed_tools":["mcp:collaborate_claim","adapter:provider-x"]},{"agent_id":"copilot","display_name":"Copilot","status":"unconfigured","branch_prefixes":["copilot/"],"allowed_tools":[]}]}`
}

func TestParseAndValidateRegistry(t *testing.T) {
	r, err := Parse([]byte(validRegistryJSON()))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Agents) != 2 {
		t.Fatalf("agents=%d, want 2", len(r.Agents))
	}
}

func TestParseRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	for _, input := range []string{
		strings.Replace(validRegistryJSON(), `"display_name":"Claude"`, `"display_name":"Claude","unexpected":true`, 1),
		validRegistryJSON() + `{}`,
	} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Fatalf("accepted invalid registry %s", input)
		}
	}
}

func TestValidateRejectsDuplicateIDsAndOverlappingPrefixes(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"duplicate ID", strings.Replace(validRegistryJSON(), `"agent_id":"copilot"`, `"agent_id":"claude"`, 1), "duplicate agent_id"},
		{"overlapping prefix", strings.Replace(validRegistryJSON(), `"copilot/"`, `"claude/review/"`, 1), "overlapping branch prefixes"},
		{"duplicate grant", strings.Replace(validRegistryJSON(), `"adapter:provider-x"]`, `"adapter:provider-x","adapter:provider-x"]`, 1), "duplicate tool grant"},
		{"unconfigured identity", strings.Replace(validRegistryJSON(), `"branch_prefixes":["copilot/"],"allowed_tools":[]`, `"github_identities":[{"account_type":"bot","login":"copilot[bot]","github_user_id":44}],"branch_prefixes":["copilot/"],"allowed_tools":[]`, 1), "unconfigured entries must omit github_identities"},
		{"missing allowlist", strings.Replace(validRegistryJSON(), `,"allowed_tools":[]`, ``, 1), "allowed_tools is required"},
		{"empty prefix list", strings.Replace(validRegistryJSON(), `"branch_prefixes":["copilot/"]`, `"branch_prefixes":[]`, 1), "branch_prefixes must not be empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.input))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestResolvePullRequestAgent(t *testing.T) {
	r, err := Parse([]byte(validRegistryJSON()))
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.ResolvePullRequestAgent(PullRequestIdentity{AccountType: "user", Login: "alice", GitHubUserID: 10}, "claude/feature")
	if err != nil || a.ID != "claude" {
		t.Fatalf("resolved=%+v err=%v", a, err)
	}
	if _, err := r.ResolvePullRequestAgent(PullRequestIdentity{AccountType: "user", Login: "unknown", GitHubUserID: 88}, "claude/feature"); err == nil {
		t.Fatal("branch prefix authenticated an unknown author")
	}
	if _, err := r.ResolvePullRequestAgent(PullRequestIdentity{AccountType: "user", Login: "alice", GitHubUserID: 10}, "copilot/feature"); err == nil {
		t.Fatal("unconfigured branch prefix was accepted")
	}
}

func TestResolveRejectsSharedIdentityWithoutMatchingPrefix(t *testing.T) {
	input := strings.Replace(validRegistryJSON(), `{"agent_id":"copilot","display_name":"Copilot","status":"unconfigured","branch_prefixes":["copilot/"],"allowed_tools":[]}`, `{"agent_id":"copilot","display_name":"Copilot","status":"active","github_identities":[{"account_type":"user","login":"alice","github_user_id":10}],"branch_prefixes":["copilot/"],"allowed_tools":[]}`, 1)
	r, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	author := PullRequestIdentity{AccountType: "user", Login: "alice", GitHubUserID: 10}
	if _, err := r.ResolvePullRequestAgent(author, "feature/no-prefix"); err == nil || !strings.Contains(err.Error(), "shared") {
		t.Fatalf("err=%v, want shared-identity ambiguity", err)
	}
	a, err := r.ResolvePullRequestAgent(author, "copilot/feature")
	if err != nil || a.ID != "copilot" {
		t.Fatalf("resolved=%+v err=%v", a, err)
	}
}

func TestAuthorizeIsExactAndFailClosed(t *testing.T) {
	r, err := Parse([]byte(validRegistryJSON()))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Authorize("claude", "mcp:collaborate_claim"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ agent, tool string }{{"claude", "mcp:collaborate_receipt"}, {"copilot", "mcp:collaborate_claim"}, {"missing", "mcp:collaborate_claim"}} {
		if err := r.Authorize(tc.agent, tc.tool); err == nil {
			t.Fatalf("agent %q unexpectedly authorized for %q", tc.agent, tc.tool)
		}
	}
	r.Agents[0].Status = "suspended"
	if err := r.Authorize("claude", "mcp:collaborate_claim"); err == nil || !strings.Contains(err.Error(), "suspended") {
		t.Fatalf("suspended agent authorization error=%v", err)
	}
}

func TestPathOverlapIncludesAncestorPaths(t *testing.T) {
	for _, pair := range [][2]string{{"docs/a.md", "docs/a.md"}, {"docs", "docs/a.md"}, {"src/pkg", "src/pkg/file.go"}} {
		if !PathOverlap(pair[0], pair[1]) {
			t.Fatalf("PathOverlap(%q,%q)=false", pair[0], pair[1])
		}
	}
	if PathOverlap("docs/a.md", "docs/b.md") || PathOverlap("src/pkg", "src/pkg2/file.go") {
		t.Fatal("unrelated paths overlapped")
	}
}

func TestFindFileConflictsDeterministic(t *testing.T) {
	got := FindFileConflicts([]string{"docs/guide.md", "internal/pkg"}, map[int][]string{8: {"docs/guide.md"}, 3: {"internal/pkg/file.go"}}, map[int]string{8: "pr8", 3: "pr3"})
	if len(got) != 2 || got[0].PR != 3 || got[1].PR != 8 {
		t.Fatalf("conflicts=%+v", got)
	}
}
