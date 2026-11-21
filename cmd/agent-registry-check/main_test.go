package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"collaborate/internal/agentregistry"
)

func writeGateFixtures(t *testing.T, authorID int64) (string, string) {
	t.Helper()
	dir := t.TempDir()
	registry := filepath.Join(dir, "agent-registry.json")
	data := `{"$schema":"./agent-registry.schema.json","schema_version":1,"agents":[{"agent_id":"claude","display_name":"Claude","status":"active","github_identities":[{"account_type":"user","login":"alice","github_user_id":10}],"branch_prefixes":["claude/"],"allowed_tools":[]},{"agent_id":"copilot","display_name":"Copilot","status":"active","github_identities":[{"account_type":"user","login":"bob","github_user_id":20}],"branch_prefixes":["copilot/"],"allowed_tools":[]}]}`
	if err := os.WriteFile(registry, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	event := filepath.Join(dir, "event.json")
	payload, err := json.Marshal(map[string]any{"action": "opened", "number": 1, "pull_request": map[string]any{
		"number": 1, "html_url": "https://github.com/acme/project/pull/1",
		"user": map[string]any{"login": "alice", "id": authorID, "type": "User"},
		"head": map[string]any{"ref": "claude/fix"},
		"base": map[string]any{"ref": "main"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(event, payload, 0600); err != nil {
		t.Fatal(err)
	}
	return registry, event
}

func TestCheckedInDefaultRegistryIsValid(t *testing.T) {
	path := filepath.Join("..", "..", ".github", "agent-registry.json")
	if _, err := agentregistry.Load(path); err != nil {
		t.Fatalf("load checked-in default registry: %v", err)
	}
}

func TestRunPassesAuthorizedNonOverlappingPRAndIgnoresUnregisteredClaims(t *testing.T) {
	registry, event := writeGateFixtures(t, 10)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/project/pulls/1/files":
			_, _ = w.Write([]byte(`[{"filename":"docs/current.md"}]`))
		case "/repos/acme/project/pulls":
			_, _ = w.Write([]byte(`[{"number":2,"html_url":"https://github.com/acme/project/pull/2","user":{"login":"bob","id":20,"type":"User"},"head":{"ref":"copilot/other"},"base":{"ref":"main"}},{"number":3,"html_url":"https://github.com/acme/project/pull/3","user":{"login":"mallory","id":88,"type":"User"},"head":{"ref":"unknown/branch"},"base":{"ref":"main"}}]`))
		case "/repos/acme/project/pulls/2/files":
			_, _ = w.Write([]byte(`[{"filename":"internal/other.go"}]`))
		case "/repos/acme/project/pulls/3/files":
			t.Error("unregistered PR file claims must not be fetched")
			http.Error(w, "unexpected", http.StatusInternalServerError)
		default:
			t.Errorf("unexpected GitHub API request %s", r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	var output strings.Builder
	if err := run(registry, event, "test-token", "acme/project", api.URL, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `authorized as "claude"`) || !strings.Contains(output.String(), "no overlaps") || !strings.Contains(output.String(), "ignored 1 unregistered PRs") {
		t.Fatalf("unexpected output: %s", output.String())
	}
}

func TestRunRejectsHierarchicalOverlap(t *testing.T) {
	registry, event := writeGateFixtures(t, 10)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/project/pulls/1/files":
			_, _ = w.Write([]byte(`[{"filename":"internal/pkg"}]`))
		case "/repos/acme/project/pulls":
			_, _ = w.Write([]byte(`[{"number":2,"html_url":"https://github.com/acme/project/pull/2","user":{"login":"bob","id":20,"type":"User"},"head":{"ref":"copilot/other"},"base":{"ref":"main"}}]`))
		case "/repos/acme/project/pulls/2/files":
			_, _ = w.Write([]byte(`[{"filename":"internal/pkg/file.go"}]`))
		default:
			t.Errorf("unexpected GitHub API request %s", r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	var output strings.Builder
	err := run(registry, event, "test-token", "acme/project", api.URL, &output)
	if err == nil || !strings.Contains(err.Error(), "overlapping") || !strings.Contains(output.String(), "PR #2") {
		t.Fatalf("err=%v output=%s, want overlap denial", err, output.String())
	}
}

func TestRunRejectsRenameOutOfClaimedPath(t *testing.T) {
	registry, event := writeGateFixtures(t, 10)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/project/pulls/1/files":
			_, _ = w.Write([]byte(`[{"filename":"internal/pkg/file.go"}]`))
		case "/repos/acme/project/pulls":
			_, _ = w.Write([]byte(`[{"number":2,"html_url":"https://github.com/acme/project/pull/2","user":{"login":"bob","id":20,"type":"User"},"head":{"ref":"copilot/other"},"base":{"ref":"main"}}]`))
		case "/repos/acme/project/pulls/2/files":
			// PR #2 renames the file out of the path PR #1 changes; only the
			// rename's previous_filename overlaps.
			_, _ = w.Write([]byte(`[{"filename":"moved/file.go","previous_filename":"internal/pkg/file.go"}]`))
		default:
			t.Errorf("unexpected GitHub API request %s", r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	var output strings.Builder
	err := run(registry, event, "test-token", "acme/project", api.URL, &output)
	if err == nil || !strings.Contains(err.Error(), "overlapping") || !strings.Contains(output.String(), "internal/pkg/file.go") || !strings.Contains(output.String(), "PR #2") {
		t.Fatalf("err=%v output=%s, want rename-source overlap denial", err, output.String())
	}
}

func TestRunRejectsUnknownAuthorBeforeGitHubAPI(t *testing.T) {
	registry, event := writeGateFixtures(t, 999)
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.NotFound(w, r)
	}))
	defer api.Close()
	err := run(registry, event, "test-token", "acme/project", api.URL, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("err=%v", err)
	}
	if calls != 0 {
		t.Fatalf("made %d API calls for rejected identity", calls)
	}
}
