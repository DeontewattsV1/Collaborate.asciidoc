package agentregistry

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func fileRows(start, n int) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"filename":"pkg/f%04d.go"}`, start+i)
	}
	return "[" + strings.Join(rows, ",") + "]"
}

func newTestClient(t *testing.T, h http.HandlerFunc) *GitHubClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &GitHubClient{BaseURL: srv.URL, Repository: "acme/project", Token: "test-token"}
}

func TestListFilesFollowsPagesUntilShortPage(t *testing.T) {
	var pages []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/project/pulls/7/files" || r.URL.Query().Get("per_page") != "100" {
			t.Errorf("unexpected request %s", r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization=%q", got)
		}
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		switch page {
		case "1":
			_, _ = w.Write([]byte(fileRows(0, githubPageSize)))
		case "2":
			_, _ = w.Write([]byte(`[{"filename":"moved/new.go","previous_filename":"pkg/old.go"}]`))
		default:
			t.Errorf("requested page %s after a short page", page)
			_, _ = w.Write([]byte(`[]`))
		}
	})
	paths, err := c.PullRequestFiles(7)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pages, ",") != "1,2" {
		t.Fatalf("pages requested=%v, want 1,2", pages)
	}
	// 100 files from page 1, plus both sides of the page-2 rename.
	if len(paths) != githubPageSize+2 {
		t.Fatalf("got %d paths, want %d", len(paths), githubPageSize+2)
	}
	joined := "," + strings.Join(paths, ",") + ","
	for _, want := range []string{",moved/new.go,", ",pkg/old.go,", ",pkg/f0000.go,", ",pkg/f0099.go,"} {
		if !strings.Contains(joined, want) {
			t.Errorf("paths missing %s", strings.Trim(want, ","))
		}
	}
	for i := 1; i < len(paths); i++ {
		if paths[i-1] >= paths[i] {
			t.Fatalf("paths not unique and sorted at %d: %q >= %q", i, paths[i-1], paths[i])
		}
	}
}

func TestListingsFailClosedAtPageCap(t *testing.T) {
	var calls int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if strings.HasSuffix(r.URL.Path, "/files") {
			_, _ = w.Write([]byte(fileRows(0, githubPageSize)))
			return
		}
		rows := make([]string, githubPageSize)
		for i := range rows {
			rows[i] = fmt.Sprintf(`{"number":%d,"user":{"login":"u","id":1,"type":"User"},"head":{"ref":"x"},"base":{"ref":"main"}}`, i+2)
		}
		_, _ = w.Write([]byte("[" + strings.Join(rows, ",") + "]"))
	})
	if _, err := c.PullRequestFiles(1); err == nil || !strings.Contains(err.Error(), "exceeded 30 pages") {
		t.Fatalf("PullRequestFiles err=%v, want page-cap error", err)
	}
	if n := atomic.SwapInt32(&calls, 0); n != maxGitHubPages {
		t.Fatalf("files listing made %d requests, want %d", n, maxGitHubPages)
	}
	if _, err := c.OtherOpenPullRequests(1, "main"); err == nil || !strings.Contains(err.Error(), "exceeded 30 pages") {
		t.Fatalf("OtherOpenPullRequests err=%v, want page-cap error", err)
	}
	if n := atomic.LoadInt32(&calls); n != maxGitHubPages {
		t.Fatalf("pulls listing made %d requests, want %d", n, maxGitHubPages)
	}
}

func TestGetRejectsOversizedAndErrorResponses(t *testing.T) {
	oversized := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("["))
		_, _ = w.Write([]byte(strings.Repeat(" ", maxGitHubResponse)))
		_, _ = w.Write([]byte("]"))
	})
	if _, err := oversized.PullRequestFiles(1); err == nil || !strings.Contains(err.Error(), "exceeded 16 MiB") {
		t.Fatalf("oversized err=%v, want size-limit error", err)
	}
	denied := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	})
	if _, err := denied.PullRequestFiles(1); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("401 err=%v, want API status error", err)
	}
	redirect := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example/steal", http.StatusFound)
	})
	if _, err := redirect.PullRequestFiles(1); err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("redirect err=%v, want redirect refused as a non-2xx status", err)
	}
}

func TestListFilesRejectsInvalidPathsFromGitHub(t *testing.T) {
	for name, body := range map[string]string{
		"parent traversal":       `[{"filename":"../etc/passwd"}]`,
		"absolute path":          `[{"filename":"/etc/passwd"}]`,
		"empty segment":          `[{"filename":"a//b.go"}]`,
		"dot segment":            `[{"filename":"a/./b.go"}]`,
		"backslash":              `[{"filename":"a\\b.go"}]`,
		"empty filename":         `[{"filename":""}]`,
		"invalid rename source":  `[{"filename":"ok.go","previous_filename":"../escape.go"}]`,
		"absolute rename source": `[{"filename":"ok.go","previous_filename":"/abs.go"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
			if _, err := c.PullRequestFiles(3); err == nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("err=%v, want invalid-path error", err)
			}
		})
	}
}

func TestOtherOpenPullRequestsFiltersAndMapsBotAuthors(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"number":1,"user":{"login":"alice","id":10,"type":"User"},"head":{"ref":"claude/x"},"base":{"ref":"main"}},
			{"number":2,"html_url":"https://github.com/acme/project/pull/2","user":{"login":"copilot[bot]","id":44,"type":"Bot"},"head":{"ref":"copilot/y"},"base":{"ref":"main"}},
			{"number":3,"user":{"login":"bob","id":20,"type":"User"},"head":{"ref":"z"},"base":{"ref":"release"}}
		]`))
	})
	pulls, err := c.OtherOpenPullRequests(1, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(pulls) != 1 {
		t.Fatalf("pulls=%+v, want only #2 (excluded #1, other base #3)", pulls)
	}
	got := pulls[0]
	want := PullRequestIdentity{AccountType: "bot", Login: "copilot[bot]", GitHubUserID: 44}
	if got.Number != 2 || got.Head != "copilot/y" || got.Author != want {
		t.Fatalf("pull=%+v, want #2 by %+v", got, want)
	}
}
