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
	// Assert the specific rejection so each case proves the path it names.
	// Trailing values are caught by the up-front object decode in
	// validateRequiredJSONFields; Parse's later "multiple JSON values" check is
	// defense in depth that well-formed input cannot reach.
	cases := []struct{ name, input, want string }{
		{"unknown field", strings.Replace(validRegistryJSON(), `"display_name":"Claude"`, `"display_name":"Claude","unexpected":true`, 1), "unknown field"},
		{"trailing object", validRegistryJSON() + `{}`, "after top-level value"},
		{"trailing scalar", validRegistryJSON() + ` 1`, "after top-level value"},
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

func TestValidateMatchesSchemaLimits(t *testing.T) {
	long := func(r string, n int) string { return strings.Repeat(r, n) }
	accept := []struct{ name, input string }{
		{"100-character non-ASCII display name", strings.Replace(validRegistryJSON(), `"display_name":"Claude"`, `"display_name":"`+long("é", 100)+`"`, 1)},
		{"maximum GitHub user ID", strings.Replace(validRegistryJSON(), `"github_user_id":10`, `"github_user_id":9007199254740991`, 1)},
	}
	for _, tc := range accept {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.input)); err != nil {
				t.Fatalf("schema-valid registry rejected: %v", err)
			}
		})
	}
	reject := []struct{ name, input, want string }{
		{"101-character display name", strings.Replace(validRegistryJSON(), `"display_name":"Claude"`, `"display_name":"`+long("é", 101)+`"`, 1), "display_name"},
		{"GitHub user ID above schema maximum", strings.Replace(validRegistryJSON(), `"github_user_id":10`, `"github_user_id":9007199254740992`, 1), "GitHub user ID"},
		{"prefix segment ending in dot", strings.Replace(validRegistryJSON(), `"copilot/"`, `"copilot./"`, 1), "branch"},
	}
	for _, tc := range reject {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.input))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestGitHubClientRejectsMalformedRepositoryWithoutPanic(t *testing.T) {
	for _, repo := range []string{"", "noslash", "owner/", "/name", "a/b/c"} {
		t.Run(repo, func(t *testing.T) {
			c := &GitHubClient{Token: "t", BaseURL: "http://127.0.0.1:1", Repository: repo}
			if _, err := c.PullRequestFiles(1); err == nil || !strings.Contains(err.Error(), "owner/name") {
				t.Fatalf("PullRequestFiles err=%v, want owner/name validation error", err)
			}
			if _, err := c.OtherOpenPullRequests(1, "main"); err == nil || !strings.Contains(err.Error(), "owner/name") {
				t.Fatalf("OtherOpenPullRequests err=%v, want owner/name validation error", err)
			}
		})
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

func botRegistryJSON() string {
	return strings.Replace(validRegistryJSON(),
		`"status":"unconfigured","branch_prefixes":["copilot/"],"allowed_tools":[]`,
		`"status":"active","github_identities":[{"account_type":"bot","login":"copilot[bot]","github_user_id":44}],"branch_prefixes":["copilot/"],"allowed_tools":[]`, 1)
}

func TestResolveBotIdentity(t *testing.T) {
	r, err := Parse([]byte(botRegistryJSON()))
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.ResolvePullRequestAgent(PullRequestIdentity{AccountType: "bot", Login: "copilot[bot]", GitHubUserID: 44}, "copilot/fix")
	if err != nil || a.ID != "copilot" {
		t.Fatalf("bot author resolved=%+v err=%v, want copilot", a, err)
	}
	// The numeric ID must match with the same account type: a user account that
	// happens to share the bot's ID is a different identity.
	if _, err := r.ResolvePullRequestAgent(PullRequestIdentity{AccountType: "user", Login: "copilot", GitHubUserID: 44}, "copilot/fix"); err == nil {
		t.Fatal("user account with a bot's numeric ID was resolved as the bot")
	}
	// A registered bot cannot borrow another agent's branch namespace.
	if _, err := r.ResolvePullRequestAgent(PullRequestIdentity{AccountType: "bot", Login: "copilot[bot]", GitHubUserID: 44}, "claude/fix"); err == nil {
		t.Fatal("bot resolved under another agent's branch prefix")
	}
}

func TestValidateRejectsMismatchedBotLogins(t *testing.T) {
	for name, input := range map[string]string{
		"bot without [bot] suffix": strings.Replace(botRegistryJSON(), `"login":"copilot[bot]"`, `"login":"copilot"`, 1),
		"user with [bot] suffix":   strings.Replace(validRegistryJSON(), `"login":"alice"`, `"login":"alice[bot]"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(input)); err == nil || !strings.Contains(err.Error(), "invalid login") {
				t.Fatalf("err=%v, want invalid login error", err)
			}
		})
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
