package ghcoord

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeGitHub serves the subset of the REST API used by Client.
type fakeGitHub struct {
	mu       sync.Mutex
	t        *testing.T
	pulls    map[int]map[string]any
	files    map[int][]string
	commits  map[int][]string
	reviews  map[int][]map[string]any
	comments []map[string]any
	nextID   int64
	readOnly bool
	srv      *httptest.Server
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{t: t, pulls: map[int]map[string]any{}, files: map[int][]string{}, commits: map[int][]string{}, reviews: map[int][]map[string]any{}, nextID: 100}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) addPull(n int, login, branch, created, body string, files ...string) {
	f.pulls[n] = map[string]any{
		"number": n, "title": fmt.Sprintf("PR %d", n), "body": body, "state": "open", "draft": false,
		"html_url": fmt.Sprintf("https://github.test/o/r/pull/%d", n), "created_at": created,
		"user": map[string]string{"login": login},
		"head": map[string]string{"ref": branch, "sha": fmt.Sprintf("sha%d", n)},
		"base": map[string]string{"ref": "main"},
	}
	f.files[n] = files
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// paginate serves one item per page so the Link-header loop is exercised.
func (f *fakeGitHub) paginate(w http.ResponseWriter, r *http.Request, items []any) {
	page := 1
	fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
	if page < 1 {
		page = 1
	}
	if page < len(items) {
		q := r.URL.Query()
		q.Set("page", fmt.Sprint(page+1))
		w.Header().Set("Link", fmt.Sprintf(`<%s%s?%s>; rel="next"`, f.srv.URL, r.URL.Path, q.Encode()))
	}
	if page > len(items) {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, items[page-1:page])
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer test-token" {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/repos/o/r")
	var n int
	switch {
	case r.Method == http.MethodGet && p == "/pulls":
		var items []any
		for i := 1; i <= 100; i++ {
			if pr, ok := f.pulls[i]; ok {
				items = append(items, pr)
			}
		}
		f.paginate(w, r, items)
	case r.Method == http.MethodGet && scan(p, "/pulls/%d/files", &n):
		var items []any
		for _, name := range f.files[n] {
			items = append(items, map[string]string{"filename": name})
		}
		f.paginate(w, r, items)
	case r.Method == http.MethodGet && scan(p, "/pulls/%d/commits", &n):
		var items []any
		for _, m := range f.commits[n] {
			items = append(items, map[string]any{"commit": map[string]string{"message": m}})
		}
		f.paginate(w, r, items)
	case r.Method == http.MethodGet && scan(p, "/pulls/%d/reviews", &n):
		var items []any
		for _, rv := range f.reviews[n] {
			items = append(items, rv)
		}
		f.paginate(w, r, items)
	case r.Method == http.MethodGet && scan(p, "/pulls/%d", &n):
		pr, ok := f.pulls[n]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, pr)
	case r.Method == http.MethodGet && scan(p, "/issues/%d/comments", &n):
		var items []any
		for _, c := range f.comments {
			items = append(items, c)
		}
		f.paginate(w, r, items)
	case r.Method == http.MethodPost && scan(p, "/issues/%d/comments", &n):
		if f.readOnly {
			http.Error(w, `{"message":"Resource not accessible by integration"}`, http.StatusForbidden)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.nextID++
		f.comments = append(f.comments, map[string]any{"id": f.nextID, "body": body["body"], "user": map[string]string{"login": "github-actions[bot]"}})
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{"id": f.nextID})
	case r.Method == http.MethodPatch && scan(p, "/issues/comments/%d", &n):
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, c := range f.comments {
			if c["id"] == int64(n) {
				c["body"] = body["body"]
				writeJSON(w, c)
				return
			}
		}
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
		http.Error(w, "unexpected", http.StatusTeapot)
	}
}

func scan(p, format string, n *int) bool {
	var rest string
	k, _ := fmt.Sscanf(p+"\x00", format+"%s", n, &rest)
	return k == 2 && rest == "\x00"
}

func TestClientCheckAndStickyComment(t *testing.T) {
	gh := newFakeGitHub(t)
	reg := mustRegistry(t, testRegistry)
	gh.addPull(3, "Copilot", "copilot/refactor", "2026-11-21T09:00:00Z", "Collaborate-Claim: internal/broker", "docs/OBSERVABILITY.md")
	gh.addPull(5, "DeontewattsV1", "claude/feature", "2026-11-21T10:00:00Z", "Adds things\nCollaborate-Claim: ../bad", "internal/broker/server.go", "README.md")
	gh.commits[5] = []string{"Feature\n\nCo-Authored-By: Claude <noreply@anthropic.com>"}
	gh.addPull(6, "dependabot[bot]", "dependabot/go_modules/x", "2026-11-21T11:00:00Z", "", "go.mod", "go.sum")
	gh.reviews[5] = []map[string]any{{"user": map[string]string{"login": "cubic-dev-ai[bot]"}, "state": "APPROVED", "commit_id": "sha5", "submitted_at": "2026-11-21T10:30:00Z"}}

	c := &Client{BaseURL: gh.srv.URL, Token: "test-token"}
	ctx := context.Background()
	rep, err := c.Check(ctx, reg, "o/r", 5, 50)
	if err != nil {
		t.Fatal(err)
	}
	if rep.PR.Attribution.AgentID != "claude" || rep.PR.Attribution.Source != "branch" {
		t.Fatalf("attribution=%+v", rep.PR.Attribution)
	}
	if len(rep.PR.Files) != 2 || len(rep.Open) != 2 {
		t.Fatalf("files=%v open=%d (pagination must collect all pages)", rep.PR.Files, len(rep.Open))
	}
	if rep.BlockingConflictCount != 1 || rep.Conflicts[0].Other.Number != 3 {
		t.Fatalf("conflicts=%+v, want PR #3's earlier directory claim to block", rep.Conflicts)
	}
	if !rep.IndependentApproval {
		t.Fatalf("cubic approval on head should count as independent: %+v", rep.Reviews)
	}
	if !strings.Contains(strings.Join(rep.Notes, "\n"), "../bad") {
		t.Fatalf("invalid claim not reported: %v", rep.Notes)
	}

	body := rep.Markdown()
	if err := c.UpsertComment(ctx, "o/r", 5, body, "github-actions[bot]"); err != nil {
		t.Fatal(err)
	}
	if err := c.UpsertComment(ctx, "o/r", 5, body+"\nupdated", "github-actions[bot]"); err != nil {
		t.Fatal(err)
	}
	if len(gh.comments) != 1 || !strings.HasSuffix(gh.comments[0]["body"].(string), "updated") {
		t.Fatalf("sticky comment not updated in place: %+v", gh.comments)
	}

	// A marker comment written by someone else is never edited.
	gh.comments[0]["user"] = map[string]string{"login": "mallory"}
	if err := c.UpsertComment(ctx, "o/r", 5, body, "github-actions[bot]"); err != nil {
		t.Fatal(err)
	}
	if len(gh.comments) != 2 || gh.comments[0]["body"].(string) == body {
		t.Fatalf("foreign marker comment was edited: %+v", gh.comments)
	}

	gh.readOnly = true
	gh.comments = nil
	if err := c.UpsertComment(ctx, "o/r", 5, body, ""); !IsPermission(err) {
		t.Fatalf("read-only comment error=%v, want permission error", err)
	}
}

func TestClientRejectsForeignNextLinkAndBadToken(t *testing.T) {
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<https://evil.example/steal>; rel="next"`)
		writeJSON(w, []any{map[string]string{"filename": "a"}})
	}))
	defer foreign.Close()
	c := &Client{BaseURL: foreign.URL, Token: "t"}
	if _, _, err := list[ghFile](context.Background(), c, "/repos/o/r/pulls/1/files"); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("foreign next link err=%v", err)
	}

	gh := newFakeGitHub(t)
	gh.addPull(1, "x", "y", "2026-11-21T09:00:00Z", "")
	bad := &Client{BaseURL: gh.srv.URL, Token: "wrong"}
	_, err := bad.Check(context.Background(), mustRegistry(t, testRegistry), "o/r", 1, 10)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("bad token err=%v", err)
	}
	if _, err := (&Client{BaseURL: gh.srv.URL}).Check(context.Background(), mustRegistry(t, testRegistry), "not-a-repo", 1, 10); err == nil {
		t.Fatal("malformed repo accepted")
	}
}
