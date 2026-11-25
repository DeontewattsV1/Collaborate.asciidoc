package guild

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"collaborate/internal/adapter"
	"collaborate/internal/agentterm"
)

func TestRegistryAgentsAreDistinctFromTools(t *testing.T) {
	reg := Load()
	need := []string{"claude", "codex", "chatgpt", "gemini", "grok", "tay", "manus", "cursor", "copilot", "operator", "ollama"}
	seenPhrase := map[string]string{}
	seenAvatar := map[string]string{}
	for _, c := range reg.All() {
		if c.Look == "" || c.Voice == "" || c.Quirk == "" || c.Catchphrase == "" || c.Role == "" || c.Talks == "" || c.Expertise == "" || c.Avatar == "" {
			t.Fatalf("%s is missing personality fields", c.ID)
		}
		if prev, ok := seenPhrase[c.Catchphrase]; ok {
			t.Fatalf("catchphrase %q shared by %s and %s", c.Catchphrase, prev, c.ID)
		}
		seenPhrase[c.Catchphrase] = c.ID
		if prev, ok := seenAvatar[c.Avatar]; ok {
			t.Fatalf("avatar %s shared by %s and %s", c.Avatar, prev, c.ID)
		}
		seenAvatar[c.Avatar] = c.ID
		prompt := c.SystemPrompt(reg.Agents())
		for _, bit := range []string{c.Catchphrase, c.Quirk, c.Talks, c.Voice, c.Role} {
			if !strings.Contains(prompt, bit) {
				t.Fatalf("%s system prompt missing %q", c.ID, bit)
			}
		}
		if !strings.Contains(c.LocalReply("hello"), c.Catchphrase) {
			t.Fatalf("%s local reply dropped the catchphrase", c.ID)
		}
		if c.Kind != "agent" && c.Kind != "tool" {
			t.Fatalf("%s kind %s", c.ID, c.Kind)
		}
	}
	for _, id := range need {
		c, ok := reg.Get(id)
		if !ok || c.Kind != "agent" {
			t.Fatalf("agent %s missing", id)
		}
	}
	cloud, ok := reg.Get("codex-cloud")
	if !ok || cloud.Kind != "tool" {
		t.Fatal("codex cloud bot should stay a tool, distinct from the Codex agent")
	}
	if reg.by["codex"].Avatar == cloud.Avatar {
		t.Fatal("Codex agent reused the cloud bot avatar")
	}
}

func TestLoopGuardStopsAPair(t *testing.T) {
	svc := testService(t, false)
	calls := 0
	svc.Complete = scripted{calls: &calls, reply: func(id string) string {
		if id == "grok" {
			return "Skip the ceremony. @claude already spoke."
		}
		return "Let me sit with that. @grok your turn."
	}}
	svc.Guard = Guard{MaxTurns: 8, MaxTokens: 100000}
	if _, err := svc.Post(context.Background(), "guild", "@claude @grok hello", true); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("character calls = %d, want 2 (each speaks once)", calls)
	}
}

func TestCostCapStopsTheRound(t *testing.T) {
	svc := testService(t, false)
	calls := 0
	svc.Complete = scripted{calls: &calls, tokens: 100, reply: func(id string) string {
		switch id {
		case "claude":
			return "Let me sit with that. @codex"
		case "codex":
			return "Small diff. Let's ship it. @gemini"
		default:
			return "Here's the other angle. @grok"
		}
	}}
	svc.Guard = Guard{MaxTurns: 4, MaxTokens: 20}
	if _, err := svc.Post(context.Background(), "guild", "@claude a small api", true); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 because the cost cap closed the round", calls)
	}
	msgs, err := svc.Messages("guild", 0)
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, m := range msgs {
		if strings.Contains(m.Text, "cost cap") {
			saw = true
		}
	}
	if !saw {
		t.Fatal("cost cap did not record a system line")
	}
}

func TestMessagesLandInAVerifiableLog(t *testing.T) {
	svc := testService(t, true)
	if _, err := svc.Post(context.Background(), "guild", "@claude @grok the chat log", true); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Entries < 3 {
		t.Fatalf("entries = %d", rep.Entries)
	}
	msgs, err := svc.Messages("guild", 0)
	if err != nil {
		t.Fatal(err)
	}
	var sawClaude, sawGrok bool
	for _, m := range msgs {
		if m.From == "claude" && strings.Contains(m.Text, "Let me sit with that.") {
			sawClaude = true
		}
		if m.From == "grok" && strings.Contains(m.Text, "Skip the ceremony.") {
			sawGrok = true
		}
	}
	if !sawClaude || !sawGrok {
		t.Fatalf("personalities missing from the log: %+v", msgs)
	}
	raw, err := os.ReadFile(filepath.Join(svc.dir, "guild.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []agentterm.Entry
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e agentterm.Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	// A rewritten body with the old hash must fail verification.
	entries[len(entries)-1].Body = `{"thread":"guild","id":"forged","kind":"message","from":"claude","text":"forged"}`
	if _, err := agentterm.VerifyChain(entries, svc.Term.PublicKey()); err == nil {
		t.Fatal("tampered chat entry was accepted")
	}
}

func TestProviderCallUsesPersonalityAndBrokerSay(t *testing.T) {
	var sawSystem bool
	var says int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			System   string `json:"system"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if strings.Contains(payload.System, "Let me sit with that.") && strings.Contains(payload.System, "Names the constraint") {
			sawSystem = true
		}
		for _, m := range payload.Messages {
			if m.Role == "system" && strings.Contains(m.Content, "Let me sit with that.") {
				sawSystem = true
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"Let me sit with that. One constraint, then the change."}]}`))
	}))
	defer server.Close()

	svc := testService(t, false)
	svc.Getenv = func(key string) string {
		switch key {
		case "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "XAI_API_KEY":
			return "fixture-key"
		case "ANTHROPIC_BASE_URL":
			return server.URL
		case "ANTHROPIC_MODEL":
			return "fixture-model"
		default:
			return ""
		}
	}
	svc.Guard = Guard{MaxTurns: 1, MaxTokens: 8000}
	svc.Complete = LiveCompleter{
		Getenv: svc.Getenv,
		HTTP:   server.Client(),
		OnSay: func(c Character, text string, tokens int) (adapter.EventAck, error) {
			says++
			if strings.Contains(text, "fixture-key") {
				t.Fatal("broker recorded the API key")
			}
			entry, err := svc.recordReply(Job{Character: c, Thread: "guild"}, text, tokens)
			if err != nil {
				return adapter.EventAck{}, err
			}
			return adapter.EventAck{Seq: entry.Seq, Hash: entry.Hash}, nil
		},
	}
	if _, err := svc.Post(context.Background(), "guild", "@claude hello", true); err != nil {
		t.Fatal(err)
	}
	if !sawSystem {
		t.Fatal("provider request did not include Claude's personality")
	}
	if says != 1 {
		t.Fatalf("broker Say calls = %d, want 1", says)
	}
	if _, err := svc.Verify(); err != nil {
		t.Fatal(err)
	}
	broker := &ledgerBroker{}
	if _, err := broker.Claim("nope", []string{"x"}); err == nil {
		t.Fatal("chat broker allowed a path claim")
	}
}

func TestHTTPRequiresOwnerTokenAndHidesSecrets(t *testing.T) {
	svc := testService(t, true)
	token := "owner-token-not-a-provider-key"
	srv := httptest.NewServer(Handler(svc.Service, token, ""))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/characters")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", res.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/characters", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var payload struct {
		Characters []CharacterView `json:"characters"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(payload)
	if strings.Contains(string(raw), token) || strings.Contains(string(raw), "sk-") {
		t.Fatal("response included a secret")
	}
	var claude CharacterView
	for _, c := range payload.Characters {
		if c.ID == "claude" {
			claude = c
		}
	}
	if claude.Catchphrase != "Let me sit with that." || !claude.Online || claude.Kind != "agent" {
		t.Fatalf("claude card = %+v", claude)
	}
}

type scripted struct {
	calls  *int
	tokens int
	reply  func(id string) string
}

func (s scripted) Complete(_ context.Context, job Job) (Result, error) {
	*s.calls++
	tokens := s.tokens
	if tokens <= 0 {
		tokens = 5
	}
	return Result{Text: s.reply(job.Character.ID), Tokens: tokens, Recorded: false}, nil
}

type testSvc struct {
	*Service
	dir string
}

func TestHoldKeepsTheProviderQuietUntilAsked(t *testing.T) {
	svc := testService(t, true)
	token := "owner-token-not-a-provider-key"
	srv := httptest.NewServer(Handler(svc.Service, token, ""))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/threads/guild/messages", strings.NewReader(`{"text":"@claude hello","reply":"hold"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d", res.StatusCode)
	}
	msgs, err := svc.Messages("guild", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].From != "owner" {
		t.Fatalf("hold still produced replies: %+v", msgs)
	}
	line, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/threads/guild/lines", strings.NewReader(`{"from":"claude","text":"Let me sit with that. Gateway said so."}`))
	line.Header.Set("Authorization", "Bearer "+token)
	line.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(line)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("line status %d", res.StatusCode)
	}
	if _, err := svc.Verify(); err != nil {
		t.Fatal(err)
	}
	msgs, _ = svc.Messages("guild", 0)
	if len(msgs) != 2 || !strings.Contains(msgs[1].Text, "Let me sit with that.") {
		t.Fatalf("recorded line = %+v", msgs)
	}
}

func TestSearchRejectsAThreadIDThatCouldChangeTheHost(t *testing.T) {
	svc := testService(t, true)
	token := "owner-token-not-a-provider-key"
	srv := httptest.NewServer(Handler(svc.Service, token, ""))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/search", strings.NewReader(`{"thread":"http://evil.example"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", res.StatusCode)
	}
}

func TestReadRepoFileIsLoggedAndRefusesSecrets(t *testing.T) {
	svc := testService(t, true)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello guild"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=1"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc.Repo = dir
	if _, err := svc.ReadRepoFile("guild", "claude", "../.env"); err == nil {
		t.Fatal("path escape was allowed")
	}
	if _, err := svc.ReadRepoFile("guild", "claude", ".env"); err == nil {
		t.Fatal("secret file was readable")
	}
	text, err := svc.ReadRepoFile("guild", "claude", "README.md")
	if err != nil || text != "hello guild" {
		t.Fatalf("read %q err=%v", text, err)
	}
	msgs, err := svc.Messages("guild", 0)
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, m := range msgs {
		if m.Tool == "read_repo_file" && strings.Contains(m.Text, "README.md") {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("tool call missing from log: %+v", msgs)
	}
	if _, err := svc.Verify(); err != nil {
		t.Fatal(err)
	}
}

func testService(t *testing.T, demo bool) *testSvc {
	t.Helper()
	dir := t.TempDir()
	svc, err := Open(dir, demo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Term.Close() })
	svc.Getenv = func(key string) string {
		if strings.Contains(key, "KEY") {
			return "test-key"
		}
		return ""
	}
	return &testSvc{Service: svc, dir: dir}
}

func TestOwnerAndGroupNamesUseNeutralDefaultsAndEnvOverrides(t *testing.T) {
	svc := testService(t, true)
	msg, err := svc.Post(context.Background(), "guild", "hello", false)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Name != "Owner" {
		t.Fatalf("default owner name = %q, want Owner", msg.Name)
	}
	svc.Getenv = func(key string) string {
		if key == "GUILD_OWNER_NAME" {
			return "Alex"
		}
		if key == "GUILD_GROUP_NAME" {
			return "Backend Crew"
		}
		return ""
	}
	if got := svc.ownerDisplayName(); got != "Alex" {
		t.Fatalf("ownerDisplayName() = %q, want Alex", got)
	}
	if got := svc.groupTitle(); got != "Backend Crew" {
		t.Fatalf("groupTitle() = %q, want Backend Crew", got)
	}
	msgs, err := svc.Messages("guild", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) == 0 || msgs[0].Name != "Alex" {
		t.Fatalf("reloaded message name = %+v, want Alex", msgs)
	}
}
