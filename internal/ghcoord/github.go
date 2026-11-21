package ghcoord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Client is a minimal GitHub REST client for the coordination check.
type Client struct {
	BaseURL string // e.g. https://api.github.com
	Token   string
	HTTP    *http.Client
	// MaxPages bounds pagination per listing (100 items per page).
	MaxPages int
}

// StatusError is a non-2xx GitHub API response.
type StatusError struct {
	Code   int
	Method string
	Path   string
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("github %s %s: HTTP %d: %s", e.Method, e.Path, e.Code, e.Body)
}

// IsPermission reports whether err is a GitHub 403/404 response, as returned
// for a read-only token (for example on a fork pull request).
func IsPermission(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && (se.Code == http.StatusForbidden || se.Code == http.StatusNotFound)
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func (c *Client) do(ctx context.Context, method, target string, body any, out any) (string, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		rd = bytes.NewReader(b)
	}
	u := target
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		u = strings.TrimRight(c.BaseURL, "/") + target
	} else if !strings.HasPrefix(target, strings.TrimRight(c.BaseURL, "/")+"/") {
		return "", fmt.Errorf("github: refusing to follow link outside %s", c.BaseURL)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "collaborate-github-check")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	h := c.HTTP
	if h == nil {
		h = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := h.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return "", &StatusError{Code: resp.StatusCode, Method: method, Path: req.URL.Path, Body: msg}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return "", fmt.Errorf("github %s %s: %w", method, req.URL.Path, err)
		}
	}
	next := ""
	if m := nextLink.FindStringSubmatch(resp.Header.Get("Link")); m != nil {
		next = m[1]
	}
	return next, nil
}

// list fetches every page of a listing, up to MaxPages. It reports whether the
// page limit cut the listing short.
func list[T any](ctx context.Context, c *Client, path string) ([]T, bool, error) {
	max := c.MaxPages
	if max <= 0 {
		max = 30
	}
	var all []T
	next := path
	for page := 0; next != ""; page++ {
		if page == max {
			return all, true, nil
		}
		var items []T
		n, err := c.do(ctx, http.MethodGet, next, nil, &items)
		if err != nil {
			return nil, false, err
		}
		all = append(all, items...)
		next = n
	}
	return all, false, nil
}

type ghUser struct {
	Login string `json:"login"`
}

type ghPull struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	HTMLURL   string    `json:"html_url"`
	Draft     bool      `json:"draft"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	User      ghUser    `json:"user"`
	Head      struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

type ghFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
}

type ghCommit struct {
	Commit struct {
		Message string `json:"message"`
	} `json:"commit"`
}

type ghReview struct {
	User        ghUser    `json:"user"`
	State       string    `json:"state"`
	CommitID    string    `json:"commit_id"`
	SubmittedAt time.Time `json:"submitted_at"`
}

type ghComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User ghUser `json:"user"`
}

func repoPath(repo string) (string, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("repository must be OWNER/NAME, got %q", repo)
	}
	return "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]), nil
}

// loadPR fetches a pull request with its changed files, trailers, and claims.
func (c *Client) loadPR(ctx context.Context, reg *Registry, base string, p ghPull) (PR, []string, error) {
	files, truncated, err := list[ghFile](ctx, c, fmt.Sprintf("%s/pulls/%d/files?per_page=100", base, p.Number))
	if err != nil {
		return PR{}, nil, err
	}
	commits, _, err := list[ghCommit](ctx, c, fmt.Sprintf("%s/pulls/%d/commits?per_page=100", base, p.Number))
	if err != nil {
		return PR{}, nil, err
	}
	paths := make([]string, 0, len(files))
	seen := map[string]bool{}
	for _, f := range files {
		for _, name := range []string{f.Filename, f.PreviousFilename} {
			if name != "" && !seen[name] {
				seen[name] = true
				paths = append(paths, name)
			}
		}
	}
	msgs := make([]string, 0, len(commits))
	for _, cm := range commits {
		msgs = append(msgs, cm.Commit.Message)
	}
	claims, invalid := ParseClaims(p.Body)
	pr := PR{
		Number: p.Number, Title: p.Title, URL: p.HTMLURL, Author: p.User.Login,
		Branch: p.Head.Ref, BaseRef: p.Base.Ref, HeadSHA: p.Head.SHA, Draft: p.Draft,
		CreatedAt: p.CreatedAt, Files: paths, Claims: claims,
		FilesTruncated: truncated || len(files) >= 3000,
	}
	pr.Attribution = reg.Attribute(p.User.Login, p.Head.Ref, TrailerEmails(msgs))
	return pr, invalid, nil
}

// Check builds the coordination report for pull request number in repo
// (OWNER/NAME). maxOpen bounds how many other open pull requests are inspected.
func (c *Client) Check(ctx context.Context, reg *Registry, repo string, number, maxOpen int) (*Report, error) {
	base, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	var cur ghPull
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", base, number), nil, &cur); err != nil {
		return nil, err
	}
	current, invalid, err := c.loadPR(ctx, reg, base, cur)
	if err != nil {
		return nil, err
	}
	pulls, openTruncated, err := list[ghPull](ctx, c, base+"/pulls?state=open&sort=created&direction=asc&per_page=100")
	if err != nil {
		return nil, err
	}
	var open []PR
	skipped := 0
	for _, p := range pulls {
		if p.Number == number {
			continue
		}
		if maxOpen > 0 && len(open) >= maxOpen {
			skipped++
			continue
		}
		pr, _, err := c.loadPR(ctx, reg, base, p)
		if err != nil {
			return nil, err
		}
		open = append(open, pr)
	}
	reviews, _, err := list[ghReview](ctx, c, fmt.Sprintf("%s/pulls/%d/reviews?per_page=100", base, number))
	if err != nil {
		return nil, err
	}
	rs := make([]Review, 0, len(reviews))
	for _, r := range reviews {
		rs = append(rs, Review{Reviewer: r.User.Login, State: r.State, CommitID: r.CommitID, SubmittedAt: r.SubmittedAt})
	}
	rep := Analyze(reg, current, open, rs)
	if cur.State != "" && cur.State != "open" {
		rep.Notes = append(rep.Notes, fmt.Sprintf("This PR is %s; it no longer holds a lease.", cur.State))
		rep.BlockingConflictCount = 0
		for i := range rep.Conflicts {
			rep.Conflicts[i].Blocking = false
		}
	}
	for _, p := range invalid {
		rep.Notes = append(rep.Notes, fmt.Sprintf("Ignored invalid claim path `%s` (claims must be repository-relative).", escapeCell(p)))
	}
	if skipped > 0 || openTruncated {
		rep.Notes = append(rep.Notes, fmt.Sprintf("Only the %d oldest other open PRs were compared; more exist.", len(open)))
	}
	return rep, nil
}

// UpsertComment creates or updates the single coordination comment on the
// pull request, identified by CommentMarker and authored by botLogin when set.
func (c *Client) UpsertComment(ctx context.Context, repo string, number int, body, botLogin string) error {
	base, err := repoPath(repo)
	if err != nil {
		return err
	}
	comments, _, err := list[ghComment](ctx, c, fmt.Sprintf("%s/issues/%d/comments?per_page=100", base, number))
	if err != nil {
		return err
	}
	for _, cm := range comments {
		if !strings.HasPrefix(cm.Body, CommentMarker) {
			continue
		}
		if botLogin != "" && !strings.EqualFold(cm.User.Login, botLogin) {
			continue
		}
		if cm.Body == body {
			return nil
		}
		_, err := c.do(ctx, http.MethodPatch, fmt.Sprintf("%s/issues/comments/%d", base, cm.ID), map[string]string{"body": body}, nil)
		return err
	}
	_, err = c.do(ctx, http.MethodPost, fmt.Sprintf("%s/issues/%d/comments", base, number), map[string]string{"body": body}, nil)
	return err
}
