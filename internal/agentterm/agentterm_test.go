package agentterm

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"collaborate/internal/agentregistry"
)

// Two agents share one GitHub user (the repository's real situation) and are
// told apart by registered branch prefixes; a bot has its own identity.
const registryJSON = `{"$schema":"./agent-registry.schema.json","schema_version":1,"agents":[
{"agent_id":"claude","display_name":"Claude","status":"active","github_identities":[{"account_type":"user","login":"owner","github_user_id":10}],"branch_prefixes":["claude/"],"allowed_tools":[]},
{"agent_id":"codex","display_name":"Codex","status":"active","github_identities":[{"account_type":"user","login":"owner","github_user_id":10}],"branch_prefixes":["codex/"],"allowed_tools":[]},
{"agent_id":"bot","display_name":"Bot","status":"active","github_identities":[{"account_type":"bot","login":"helper[bot]","github_user_id":20}],"branch_prefixes":["bot/"],"allowed_tools":[]}]}`

type fakeAuth map[string]GitHubUser

func (f fakeAuth) User(_ context.Context, token string) (GitHubUser, error) {
	u, ok := f[token]
	if !ok {
		return GitHubUser{}, errors.New("GitHub rejected the token (HTTP 401)")
	}
	return u, nil
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type rig struct {
	t      *testing.T
	term   *Terminal
	srv    *Server
	http   *httptest.Server
	clock  *clock
	key    ed25519.PrivateKey
	log    string
	policy ExecPolicy
}

func newRig(t *testing.T, policy ExecPolicy) *rig {
	t.Helper()
	reg, err := agentregistry.Parse([]byte(registryJSON))
	if err != nil {
		t.Fatal(err)
	}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	r := &rig{t: t, clock: &clock{t: time.Now().UTC()}, key: key, log: filepath.Join(t.TempDir(), "log.jsonl"), policy: policy}
	r.term, err = Open(r.log, key, NewState(reg, time.Hour, policy), r.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	r.srv = &Server{T: r.term, Registry: reg, Exec: policy, Auth: fakeAuth{
		"owner-token": {Login: "owner", ID: 10, Type: "User"},
		"bot-token":   {Login: "helper[bot]", ID: 20, Type: "Bot"},
		"stranger":    {Login: "mallory", ID: 99, Type: "User"},
	}}
	r.http = httptest.NewServer(r.srv.Handler())
	t.Cleanup(func() { r.http.Close(); r.srv.Shutdown(); r.term.Close() })
	return r
}

func (r *rig) agent(token, instance, branch string) *Client {
	r.t.Helper()
	c := &Client{Server: r.http.URL, Path: filepath.Join(r.t.TempDir(), "session.json")}
	if _, err := c.ClockIn(context.Background(), token, instance, branch); err != nil {
		r.t.Fatalf("clock-in %s on %s: %v", instance, branch, err)
	}
	return c
}

func mustPost(t *testing.T, c *Client, kind string, body any) Entry {
	t.Helper()
	e, err := c.Post(context.Background(), kind, body)
	if err != nil {
		t.Fatalf("%s: %v", kind, err)
	}
	return e
}

func mustRefuse(t *testing.T, c *Client, kind string, body any, want string) {
	t.Helper()
	_, err := c.Post(context.Background(), kind, body)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("%s: err=%v, want refusal containing %q", kind, err, want)
	}
}

var (
	shaA = strings.Repeat("a", 40)
	shaB = strings.Repeat("b", 40)
)

func TestClockInIdentityIsVerifiedNotClaimed(t *testing.T) {
	r := newRig(t, ExecPolicy{})
	ctx := context.Background()
	claude := r.agent("owner-token", "claude-1", "claude/terminal")
	if claude.sess.Agent != "claude" {
		t.Fatalf("resolved %q", claude.sess.Agent)
	}
	if r.agent("owner-token", "codex-1", "codex/review").sess.Agent != "codex" {
		t.Fatal("codex prefix should resolve to codex")
	}
	cases := []struct{ token, branch, want string }{
		{"stranger", "claude/x", "not an active registered agent"},
		{"bad-token", "claude/x", "GitHub rejected"},
		{"bot-token", "claude/x", "does not"},                // a bot cannot act under Claude's prefix
		{"owner-token", "main", "branch prefix is required"}, // shared account needs a registered prefix
		{"owner-token", "copilot/x", "branch prefix is required"},
	}
	for _, tc := range cases {
		c := &Client{Server: r.http.URL}
		if _, err := c.ClockIn(ctx, tc.token, "x-1", tc.branch); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s on %s: err=%v want %q", tc.token, tc.branch, err, tc.want)
		}
	}
	// A clock-in not signed by the key it registers is refused.
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	n := time.Now().UnixMilli()
	req := ClockInRequest{Instance: "i", Branch: "claude/x", PubKey: hex.EncodeToString(pub), Nonce: n,
		Sig: hex.EncodeToString(ed25519.Sign(other, ClockInMessage("i", "claude/x", hex.EncodeToString(pub), n)))}
	if err := (&Client{Server: r.http.URL}).do(ctx, http.MethodPost, "/api/clock-in", "Bearer owner-token", req, nil); err == nil || !strings.Contains(err.Error(), "signed by the session key") {
		t.Fatalf("unsigned clock-in: %v", err)
	}
}

func TestRequestsAreBoundToTheSessionKey(t *testing.T) {
	r := newRig(t, ExecPolicy{})
	ctx := context.Background()
	claude := r.agent("owner-token", "claude-1", "claude/terminal")
	codex := r.agent("owner-token", "codex-1", "codex/review")
	mustPost(t, claude, KindNote, map[string]string{"text": "hello"})

	// Codex signs a request naming Claude's session: wrong key.
	forged := &Client{Server: codex.Server, sess: &SessionFile{Session: claude.sess.Session, Seed: codex.sess.Seed, Nonce: codex.sess.Nonce}}
	mustRefuse(t, forged, KindNote, map[string]string{"text": "I am claude"}, "not signed by this session's key")

	// Replaying a captured request (same nonce, same signature) is refused.
	seed, _ := hex.DecodeString(claude.sess.Seed)
	body := MustBody(map[string]string{"text": "once"})
	nonce := claude.sess.Nonce + 1000
	req := EntryRequest{Session: claude.sess.Session, Kind: KindNote, Nonce: nonce, Body: body,
		Sig: hex.EncodeToString(ed25519.Sign(ed25519.NewKeyFromSeed(seed), RequestMessage(claude.sess.Session, KindNote, nonce, body)))}
	if err := claude.do(ctx, http.MethodPost, "/api/entries", "", req, nil); err != nil {
		t.Fatal(err)
	}
	if err := claude.do(ctx, http.MethodPost, "/api/entries", "", req, nil); err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("replay: %v", err)
	}
	// Server-only kinds cannot be posted, and non-compact bodies are refused.
	req.Kind, req.Nonce = KindExecResult, nonce+1
	if err := claude.do(ctx, http.MethodPost, "/api/entries", "", req, nil); err == nil {
		t.Fatal("agent posted a server-only kind")
	}
	req.Kind, req.Body = KindNote, `{"text": "spaced"}`
	if err := claude.do(ctx, http.MethodPost, "/api/entries", "", req, nil); err == nil || !strings.Contains(err.Error(), "compact") {
		t.Fatalf("non-compact body: %v", err)
	}
}

func TestClaimsPreventDuplicateWork(t *testing.T) {
	r := newRig(t, ExecPolicy{})
	claude := r.agent("owner-token", "claude-1", "claude/terminal")
	codex := r.agent("owner-token", "codex-1", "codex/review")
	claude2 := r.agent("owner-token", "claude-2", "claude/other")
	held := mustPost(t, claude, KindClaim, map[string][]string{"paths": {"internal/agentterm"}})
	mustRefuse(t, codex, KindClaim, map[string][]string{"paths": {"internal/agentterm/state.go"}}, "held by claude/claude-1")
	mustRefuse(t, codex, KindClaim, map[string][]string{"paths": {"internal"}}, "overlaps")
	mustRefuse(t, claude2, KindClaim, map[string][]string{"paths": {"internal/agentterm"}}, "held by claude/claude-1") // even the same agent's other worker
	mustRefuse(t, codex, KindRelease, map[string][]string{"paths": {"internal/agentterm"}}, "not claimed by this instance")
	mustRefuse(t, codex, KindClaim, map[string][]string{"paths": {"../etc"}}, "clean repository-relative")
	mustRefuse(t, codex, KindClaim, map[string][]string{"paths": {"/abs"}}, "clean repository-relative")
	mustPost(t, codex, KindClaim, map[string][]string{"paths": {"docs/AGENT-TERMINAL.md"}})

	// Commits must be on the agent's own prefix and inside its claims.
	mustRefuse(t, claude, KindCommit, map[string]any{"sha": shaA, "branch": "main"}, "never records work on main")
	mustRefuse(t, claude, KindCommit, map[string]any{"sha": shaA, "branch": "codex/review"}, "registered prefix")
	mustRefuse(t, claude, KindCommit, map[string]any{"sha": "abc123", "branch": "claude/terminal"}, "40-character")
	mustRefuse(t, claude, KindCommit, map[string]any{"sha": shaA, "branch": "claude/terminal", "paths": []string{"docs/AGENT-TERMINAL.md"}}, "not covered by a claim")
	mustPost(t, claude, KindCommit, map[string]any{"sha": shaA, "branch": "claude/terminal", "pr": 60, "paths": []string{"internal/agentterm/state.go"}})

	mustPost(t, claude, KindRelease, map[string][]string{"paths": {"internal/agentterm"}})
	mustPost(t, codex, KindClaim, map[string][]string{"paths": {"internal/agentterm/state.go"}})
	if held.Seq <= 0 {
		t.Fatal("claim entry has no sequence")
	}
}

func TestReviewFindingsCannotBeClearedByTheAuthor(t *testing.T) {
	r := newRig(t, ExecPolicy{})
	claude := r.agent("owner-token", "claude-1", "claude/terminal")
	codex := r.agent("owner-token", "codex-1", "codex/review")
	mustRefuse(t, codex, KindFinding, map[string]any{"pr": 60, "commit": "HEAD", "severity": "major", "text": "x"}, "40-character")
	mustRefuse(t, codex, KindFinding, map[string]any{"pr": 60, "commit": shaA, "severity": "urgent", "text": "x"}, "severity")
	f := mustPost(t, codex, KindFinding, map[string]any{"pr": 60, "commit": shaA, "severity": "major", "path": "internal/agentterm/state.go", "line": 9, "text": "nonce not checked"})
	mustRefuse(t, claude, KindFindingStatus, map[string]any{"finding": f.Seq, "status": "accepted"}, "only the reviewer")
	mustRefuse(t, codex, KindFindingStatus, map[string]any{"finding": f.Seq, "status": "addressed", "commit": shaB}, "cannot mark their own")
	mustRefuse(t, claude, KindFindingStatus, map[string]any{"finding": f.Seq, "status": "addressed"}, "fixing commit")
	mustRefuse(t, codex, KindFindingStatus, map[string]any{"finding": f.Seq, "status": "accepted"}, "addressed before")
	mustPost(t, claude, KindFindingStatus, map[string]any{"finding": f.Seq, "status": "addressed", "commit": shaB})
	mustPost(t, codex, KindFindingStatus, map[string]any{"finding": f.Seq, "status": "accepted"})
	snap := r.term.Snapshot()
	if len(snap.Findings) != 1 || snap.Findings[0].Status != "accepted" || snap.Findings[0].FixedIn != shaB {
		t.Fatalf("findings: %+v", snap.Findings)
	}
}

func TestRejoinKeepsClaimsAndRetiresTheOldKey(t *testing.T) {
	r := newRig(t, ExecPolicy{})
	first := r.agent("owner-token", "claude-1", "claude/terminal")
	mustPost(t, first, KindClaim, map[string][]string{"paths": {"internal/agentterm"}})
	second := r.agent("owner-token", "claude-1", "claude/terminal") // agent restarted and rejoined
	mustRefuse(t, first, KindNote, map[string]string{"text": "stale"}, "clock in again")
	snap := r.term.Snapshot()
	if len(snap.Claims) != 1 || snap.Claims[0].Instance != "claude-1" {
		t.Fatalf("claims after rejoin: %+v", snap.Claims)
	}
	mustPost(t, second, KindRelease, map[string][]string{"paths": {"internal/agentterm"}})
	mustPost(t, second, KindClaim, map[string][]string{"paths": {"cmd/agentterm"}})
	mustPost(t, second, KindClockOut, map[string]string{})
	if snap := r.term.Snapshot(); len(snap.Claims) != 0 {
		t.Fatalf("clock-out must release claims: %+v", snap.Claims)
	}
	mustRefuse(t, second, KindNote, map[string]string{"text": "after"}, "clock in again")
}

func TestExpiryReleasesClaims(t *testing.T) {
	r := newRig(t, ExecPolicy{})
	claude := r.agent("owner-token", "claude-1", "claude/terminal")
	codex := r.agent("owner-token", "codex-1", "codex/review")
	mustPost(t, claude, KindClaim, map[string][]string{"paths": {"internal/agentterm"}})
	r.clock.add(30 * time.Minute)
	mustPost(t, codex, KindHeartbeat, map[string]string{})
	r.clock.add(31 * time.Minute)
	r.term.ExpireStale() // claude silent 61m > 1h TTL; codex silent 31m
	snap := r.term.Snapshot()
	open := 0
	for _, s := range snap.Sessions {
		if s.Open {
			open++
			if s.Agent != "codex" {
				t.Fatalf("unexpected open session %+v", s)
			}
		}
	}
	if open != 1 || len(snap.Claims) != 0 {
		t.Fatalf("open=%d claims=%+v", open, snap.Claims)
	}
	mustPost(t, codex, KindClaim, map[string][]string{"paths": {"internal/agentterm"}})
}

func TestLogSurvivesRestartAndRefusesTampering(t *testing.T) {
	r := newRig(t, ExecPolicy{})
	claude := r.agent("owner-token", "claude-1", "claude/terminal")
	mustPost(t, claude, KindClaim, map[string][]string{"paths": {"internal/agentterm"}})
	mustPost(t, claude, KindNote, map[string]string{"text": "restart test"})
	rep, err := r.term.Verify()
	if err != nil || rep.Entries != 4 || rep.AgentSigned != 3 {
		t.Fatalf("verify: %+v %v", rep, err)
	}
	r.term.Close()
	reg, _ := agentregistry.Parse([]byte(registryJSON))
	again, err := Open(r.log, r.key, NewState(reg, time.Hour, ExecPolicy{}), r.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	if snap := again.Snapshot(); snap.Head != rep.Head || len(snap.Claims) != 1 {
		t.Fatalf("replayed state differs: %+v", snap)
	}
	again.Close()

	data, _ := os.ReadFile(r.log)
	tampered := strings.Replace(string(data), "restart test", "rewritten!!!", 1)
	if err := os.WriteFile(r.log, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(r.log, r.key, NewState(reg, time.Hour, ExecPolicy{}), r.clock.now); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("tampered log loaded: %v", err)
	}
	_, otherKey, _ := ed25519.GenerateKey(rand.Reader)
	if err := os.WriteFile(r.log, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(r.log, otherKey, NewState(reg, time.Hour, ExecPolicy{}), r.clock.now); err == nil || !strings.Contains(err.Error(), "server signature") {
		t.Fatalf("log loaded under a different server key: %v", err)
	}
}

func TestOfflineVerifierCatchesForgeryEvenWithTheServerKey(t *testing.T) {
	r := newRig(t, ExecPolicy{})
	claude := r.agent("owner-token", "claude-1", "claude/terminal")
	mustPost(t, claude, KindNote, map[string]string{"text": "real"})
	entries, _ := r.term.Since(-1, 0)
	// A malicious server operator rewrites an agent entry and re-signs the
	// chain with the server key; the agent signature still exposes it.
	forged := append([]Entry(nil), entries...)
	forged[2].Body = MustBody(map[string]string{"text": "forged"})
	for i := 2; i < len(forged); i++ {
		forged[i].Prev = forged[i-1].Hash
		forged[i].Hash = ComputeHash(forged[i])
		forged[i].ServerSig = hex.EncodeToString(ed25519.Sign(r.key, []byte(forged[i].Hash)))
	}
	if _, err := VerifyChain(forged, r.term.PublicKey()); err == nil || !strings.Contains(err.Error(), "session's key") {
		t.Fatalf("server-side forgery not detected: %v", err)
	}
	if _, err := VerifyChain(entries[1:], r.term.PublicKey()); err == nil {
		t.Fatal("truncated log verified")
	}
	if _, err := VerifyChain(entries, r.term.PublicKey()); err != nil {
		t.Fatal(err)
	}
}

func TestStreamReplaysAndResumes(t *testing.T) {
	r := newRig(t, ExecPolicy{})
	claude := r.agent("owner-token", "claude-1", "claude/terminal")
	for i := 0; i < 3; i++ {
		mustPost(t, claude, KindNote, map[string]string{"text": "n"})
	}
	read := func(lastEventID string, n int) []Entry {
		req, _ := http.NewRequest(http.MethodGet, r.http.URL+"/api/stream?since=-1", nil)
		if lastEventID != "" {
			req.Header.Set("Last-Event-ID", lastEventID)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		resp, err := http.DefaultClient.Do(req.WithContext(ctx))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var got []Entry
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
		for len(got) < n && sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "data: ") {
				var e Entry
				_ = json.Unmarshal([]byte(line[6:]), &e)
				got = append(got, e)
			}
		}
		return got
	}
	all := read("", 5)
	if len(all) != 5 || all[0].Kind != KindGenesis {
		t.Fatalf("replay: %d entries", len(all))
	}
	resumed := read("2", 2) // reconnect after seeing #2
	if len(resumed) != 2 || resumed[0].Seq != 3 {
		t.Fatalf("resume: %+v", resumed)
	}
	// Live delivery to a tailing client.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := make(chan Entry, 1)
	go func() {
		_ = (&Client{Server: r.http.URL}).Tail(ctx, 4, func(e Entry) error { got <- e; cancel(); return nil })
	}()
	time.Sleep(100 * time.Millisecond)
	mustPost(t, claude, KindNote, map[string]string{"text": "live"})
	select {
	case e := <-got:
		if e.Seq != 5 {
			t.Fatalf("live entry %d", e.Seq)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no live entry")
	}
}

func TestReadTokenAndUI(t *testing.T) {
	r := newRig(t, ExecPolicy{})
	r.srv.ReadToken = "s3cret"
	for _, p := range []string{"/", "/api/state", "/api/log", "/api/verify"} {
		resp, _ := http.Get(r.http.URL + p)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without token: %d", p, resp.StatusCode)
		}
	}
	resp, err := http.Get(r.http.URL + "/?token=s3cret")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("ui: %v %v", err, resp)
	}
	resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatal("ui must be served with a restrictive CSP")
	}
}

func TestExecPolicy(t *testing.T) {
	on := ExecPolicy{Enabled: true}
	for _, argv := range [][]string{
		{"go", "test", "./..."}, {"go", "vet", "./internal/agentterm"}, {"gofmt", "-l", "cmd", "internal"},
		{"git", "status"}, {"git", "diff", "--stat"}, {"python3", "-m", "unittest", "discover", "-s", "tests"},
		{"go", "test", "-count=1", "-run", "TestX", "./..."},
	} {
		if err := on.Check(argv); err != nil {
			t.Errorf("%v refused: %v", argv, err)
		}
	}
	for _, argv := range [][]string{
		{"git", "push", "origin", "main"}, {"git", "merge", "x"}, {"git", "commit", "-m", "x"}, {"git", "reset", "--hard"},
		{"git", "rebase", "main"}, {"gh", "pr", "merge"}, {"sh", "-c", "git push"}, {"bash"}, {"curl", "x"},
		{"go", "run", "."}, {"go", "test", "-exec", "sh"}, {"go", "test", "-toolexec=sh"}, {"go", "build", "-o", "/tmp/x"},
		{"go", "vet", "-vettool=/bin/sh"}, {"gofmt", "-w", "x.go"}, {"gofmt", "-l", "-w", "x.go"}, {"git", "diff", "--output=/tmp/x"},
		{"git", "log", "../../etc"}, {"git", "show", "/etc/passwd"}, {"python3", "-c", "print(1)"}, {"python3", "-m", "http.server"},
		{"go", "test", "-C", "/"}, {"go", "build", "-pkgdir=/tmp"},
	} {
		if err := on.Check(argv); err == nil {
			t.Errorf("%v allowed", argv)
		}
	}
	if err := (ExecPolicy{}).Check([]string{"go", "version"}); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled policy: %v", err)
	}
}

func TestServerExecutesAndRecordsResult(t *testing.T) {
	dir := t.TempDir()
	r := newRig(t, ExecPolicy{Enabled: true, Dir: dir, Timeout: time.Minute})
	t.Setenv("AGENTTERM_TEST_SECRET", "do-not-leak")
	claude := r.agent("owner-token", "claude-1", "claude/terminal")
	req := mustPost(t, claude, KindExecRequest, ExecRequest{Argv: []string{"go", "version"}})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := r.term.Since(req.Seq, 0)
		for _, e := range entries {
			if e.Kind != KindExecResult {
				continue
			}
			var res ExecResult
			_ = json.Unmarshal([]byte(e.Body), &res)
			if res.Request != req.Seq || res.Exit != 0 || !strings.Contains(res.Stdout, "go version") || res.Verified != "server-executed" {
				t.Fatalf("result: %+v", res)
			}
			if _, err := r.term.Verify(); err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no exec_result")
}

func TestScrubbedEnvDropsCredentials(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_x")
	t.Setenv("ANTHROPIC_API_KEY", "sk-x")
	for _, kv := range scrubbedEnv() {
		if strings.HasPrefix(kv, "GITHUB_TOKEN=") || strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			t.Fatalf("credential leaked into exec env: %s", kv)
		}
	}
}

func TestGitHubAuthUsesTheUserEndpoint(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" || r.Header.Get("Authorization") != "Bearer good" {
			http.Error(w, "nope", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"login":"owner","id":10,"type":"User"}`))
	}))
	defer api.Close()
	g := GitHubAuth{APIURL: api.URL}
	u, err := g.User(context.Background(), "good")
	if err != nil || u.ID != 10 || u.Login != "owner" {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := g.User(context.Background(), "bad"); err == nil {
		t.Fatal("bad token accepted")
	}
}

func TestCheckedInRegistryAuthorizesTerminalAgents(t *testing.T) {
	reg, err := agentregistry.Load(filepath.Join("..", "..", ".github", "agent-registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewState(reg, time.Hour, ExecPolicy{})
	for _, tc := range []struct{ agent, branch string }{{"claude", "claude/agent-terminal"}, {"codex", "codex/x"}, {"manus", "manus/x"}} {
		if !s.branchAllowed(tc.agent, tc.branch) {
			t.Errorf("%s should be allowed on %s", tc.agent, tc.branch)
		}
	}
	if s.branchAllowed("claude", "main") || s.branchAllowed("claude", "codex/x") || s.branchAllowed("claude", "claude/") {
		t.Fatal("branch guard too loose")
	}
}
