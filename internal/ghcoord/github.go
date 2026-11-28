package ghcoord

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
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
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	HTMLURL   string     `json:"html_url"`
	Draft     bool       `json:"draft"`
	State     string     `json:"state"`
	Merged    bool       `json:"merged"`
	MergedAt  *time.Time `json:"merged_at"`
	CreatedAt time.Time  `json:"created_at"`
	User      ghUser     `json:"user"`
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

// repoPattern matches GitHub's owner and repository name rules.
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}$`)

// ValidRepo reports whether repo is a well-formed OWNER/NAME.
func ValidRepo(repo string) bool {
	if !repoPattern.MatchString(repo) {
		return false
	}
	name := repo[strings.Index(repo, "/")+1:]
	return name != "." && name != ".."
}

func repoPath(repo string) (string, error) {
	if !ValidRepo(repo) {
		return "", fmt.Errorf("repository must be OWNER/NAME, got %q", repo)
	}
	parts := strings.Split(repo, "/")
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
	sort.Strings(paths)
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

// CheckOptions configures one coordination check.
type CheckOptions struct {
	Repo   string // OWNER/NAME
	Number int    // pull request number
	// MaxOpen bounds how many other open pull requests are compared. When
	// more exist the result is INDETERMINATE, never PASS. Zero means 100.
	MaxOpen int
	// ExpectedHeadSHA is the head SHA from the triggering event, if known. A
	// mismatch means the event is stale and the result is INDETERMINATE.
	ExpectedHeadSHA string
	// Reconcile re-checks other open PRs that overlap the checked PR, so a
	// close, merge, or narrowed lease clears their stale results.
	Reconcile bool
	// MaxReconcile bounds how many PRs are re-checked. Zero means 20.
	MaxReconcile int
}

// Result is a check of one PR plus any reconciled re-checks.
type Result struct {
	Report     *Report
	Reconciled []*Report
}

// prState maps GitHub's state and merged flags to open/closed/merged.
func prState(p ghPull) string {
	switch {
	case p.Merged || p.MergedAt != nil:
		return "merged"
	case p.State == "" || p.State == "open":
		return "open"
	default:
		return "closed"
	}
}

type loadedPR struct {
	pr      PR
	invalid []string
}

// snapshot is every open PR loaded once, shared by the main check and any
// reconciliation re-checks.
type snapshot struct {
	open          []loadedPR
	listed        int // other open PRs listed (excluding the checked PR)
	listTruncated bool
	skipped       int
}

func (c *Client) loadSnapshot(ctx context.Context, reg *Registry, base string, exclude, maxOpen int) (*snapshot, error) {
	pulls, truncated, err := list[ghPull](ctx, c, base+"/pulls?state=open&sort=created&direction=asc&per_page=100")
	if err != nil {
		return nil, err
	}
	snap := &snapshot{listTruncated: truncated}
	for _, p := range pulls {
		if p.Number == exclude || prState(p) != "open" {
			continue
		}
		snap.listed++
		if len(snap.open) >= maxOpen {
			snap.skipped++
			continue
		}
		pr, invalid, err := c.loadPR(ctx, reg, base, p)
		if err != nil {
			return nil, err
		}
		snap.open = append(snap.open, loadedPR{pr: pr, invalid: invalid})
	}
	return snap, nil
}

func (c *Client) reviews(ctx context.Context, base string, number int) ([]Review, bool, error) {
	reviews, truncated, err := list[ghReview](ctx, c, fmt.Sprintf("%s/pulls/%d/reviews?per_page=100", base, number))
	if err != nil {
		return nil, false, err
	}
	rs := make([]Review, 0, len(reviews))
	for _, r := range reviews {
		rs = append(rs, Review{Reviewer: r.User.Login, State: r.State, CommitID: r.CommitID, SubmittedAt: r.SubmittedAt})
	}
	return rs, truncated, nil
}

// report analyzes target against others and records coverage.
func (c *Client) report(ctx context.Context, reg *Registry, base, repo string, target loadedPR, state string, others []PR, listed int, snap *snapshot) (*Report, error) {
	rs, reviewsTruncated, err := c.reviews(ctx, base, target.pr.Number)
	if err != nil {
		return nil, err
	}
	rep := Analyze(reg, target.pr, others, rs)
	rep.Repository = repo
	rep.PRState = state
	rep.Coverage.OtherOpenListed = listed
	if state != "open" {
		rep.LeaseHeld = false
		rep.Notes = append(rep.Notes, fmt.Sprintf("This PR is %s; it no longer holds a lease.", state))
		rep.BlockingConflictCount = 0
		for i := range rep.Conflicts {
			rep.Conflicts[i].Blocking = false
		}
	}
	for _, p := range target.invalid {
		rep.Notes = append(rep.Notes, fmt.Sprintf("Ignored invalid claim path `%s` (claims must be repository-relative).", escapeCell(p)))
	}
	if snap.skipped > 0 {
		rep.AddGap("open_pr_limit")
		rep.Notes = append(rep.Notes, fmt.Sprintf("Only the %d oldest other open PRs were compared; %d more were not, so this is not a verified pass.", len(snap.open), snap.skipped))
	}
	if snap.listTruncated {
		rep.AddGap("open_pr_listing_truncated")
		rep.Notes = append(rep.Notes, "The open-PR listing hit the page limit; some open PRs were not compared.")
	}
	if reviewsTruncated && rep.EnforceIndependent {
		rep.AddGap("reviews_truncated")
	}
	return rep, nil
}

// Check builds the coordination report for pull request number in repo
// (OWNER/NAME). maxOpen bounds how many other open pull requests are inspected.
func (c *Client) Check(ctx context.Context, reg *Registry, repo string, number, maxOpen int) (*Report, error) {
	res, err := c.Run(ctx, reg, CheckOptions{Repo: repo, Number: number, MaxOpen: maxOpen})
	if err != nil {
		return nil, err
	}
	return res.Report, nil
}

// Run performs a full check with optional reconciliation.
func (c *Client) Run(ctx context.Context, reg *Registry, opt CheckOptions) (*Result, error) {
	base, err := repoPath(opt.Repo)
	if err != nil {
		return nil, err
	}
	if opt.Number <= 0 {
		return nil, fmt.Errorf("pull request number must be positive, got %d", opt.Number)
	}
	if opt.MaxOpen <= 0 {
		opt.MaxOpen = 100
	}
	if opt.MaxReconcile <= 0 {
		opt.MaxReconcile = 20
	}
	var cur ghPull
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", base, opt.Number), nil, &cur); err != nil {
		return nil, err
	}
	state := prState(cur)
	current, invalid, err := c.loadPR(ctx, reg, base, cur)
	if err != nil {
		return nil, err
	}
	snap, err := c.loadSnapshot(ctx, reg, base, opt.Number, opt.MaxOpen)
	if err != nil {
		return nil, err
	}
	others := make([]PR, 0, len(snap.open))
	for _, o := range snap.open {
		others = append(others, o.pr)
	}
	rep, err := c.report(ctx, reg, base, opt.Repo, loadedPR{pr: current, invalid: invalid}, state, others, snap.listed, snap)
	if err != nil {
		return nil, err
	}
	if opt.ExpectedHeadSHA != "" && !strings.EqualFold(opt.ExpectedHeadSHA, current.HeadSHA) {
		rep.AddGap("stale_event_head")
		rep.Notes = append(rep.Notes, fmt.Sprintf("The triggering event was for head `%s`, but the PR head is now `%s`; a newer run will report on the current head.", shortSHA(opt.ExpectedHeadSHA), shortSHA(current.HeadSHA)))
	}
	// Re-read the PR: a push or state change during the scan makes the
	// evidence stale, which must not be reported as a pass.
	var after ghPull
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", base, opt.Number), nil, &after); err != nil {
		return nil, err
	}
	if after.Head.SHA != cur.Head.SHA || prState(after) != state {
		rep.AddGap("pr_changed_during_scan")
		rep.Notes = append(rep.Notes, "The PR changed while it was being checked; a newer run will report on the current head.")
	}
	rep.Decide()
	rep.normalize()
	res := &Result{Report: rep}
	if !opt.Reconcile {
		return res, nil
	}
	affected := map[int]bool{}
	for _, cf := range rep.Conflicts {
		affected[cf.Other.Number] = true
	}
	for _, o := range snap.open {
		if !affected[o.pr.Number] {
			continue
		}
		if len(res.Reconciled) >= opt.MaxReconcile {
			rep.Notes = append(rep.Notes, fmt.Sprintf("Only %d overlapping PRs were re-checked.", opt.MaxReconcile))
			break
		}
		tOthers := make([]PR, 0, len(snap.open))
		for _, x := range snap.open {
			if x.pr.Number != o.pr.Number {
				tOthers = append(tOthers, x.pr)
			}
		}
		if state == "open" {
			tOthers = append(tOthers, current)
		}
		listed := snap.listed - 1
		if state == "open" {
			listed++
		}
		tr, err := c.report(ctx, reg, base, opt.Repo, o, "open", tOthers, listed, snap)
		if err != nil {
			return nil, err
		}
		tr.Decide()
		tr.normalize()
		res.Reconciled = append(res.Reconciled, tr)
	}
	return res, nil
}

// FetchRegistry reads the registry file at path from ref through the
// contents API, so a check never needs the (possibly untrusted) workspace.
// found is false when the file does not exist at ref.
func (c *Client) FetchRegistry(ctx context.Context, repo, path, ref string) (reg *Registry, found bool, err error) {
	base, err := repoPath(repo)
	if err != nil {
		return nil, false, err
	}
	clean, ok := normalizePath(path)
	if !ok {
		return nil, false, fmt.Errorf("registry path %q must be repository-relative", path)
	}
	segs := strings.Split(clean, "/")
	for i := range segs {
		segs[i] = url.PathEscape(segs[i])
	}
	var out struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	target := fmt.Sprintf("%s/contents/%s?ref=%s", base, strings.Join(segs, "/"), url.QueryEscape(ref))
	if _, err := c.do(ctx, http.MethodGet, target, nil, &out); err != nil {
		var se *StatusError
		if errors.As(err, &se) && se.Code == http.StatusNotFound {
			return nil, false, nil
		}
		return nil, false, err
	}
	if out.Type != "file" || out.Encoding != "base64" {
		return nil, false, fmt.Errorf("registry %s@%s is not a regular file", clean, ref)
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(out.Content, "\n", ""))
	if err != nil {
		return nil, false, fmt.Errorf("registry %s@%s: %w", clean, ref, err)
	}
	reg, err = ParseRegistry(data)
	if err != nil {
		return nil, true, err
	}
	return reg, true, nil
}

// StatusContext is the commit status context the checker reports under.
const StatusContext = "collaborate/coordination"

// StatusState maps an outcome to a commit status state. INDETERMINATE is a
// failure unless allowIndeterminate is set.
func StatusState(o Outcome, allowIndeterminate bool) (state, description string) {
	switch o {
	case OutcomePass:
		return "success", "Complete scan: no blocking coordination issue"
	case OutcomeBlocked:
		return "failure", "Blocked: overlapping lease or required review"
	case OutcomeIndeterminate:
		if allowIndeterminate {
			return "success", "Incomplete scan (allowed by policy): not verified"
		}
		return "failure", "Incomplete scan: not a verified pass"
	default:
		return "error", "Coordination check could not run"
	}
}

// SetStatus records a commit status on sha.
func (c *Client) SetStatus(ctx context.Context, repo, sha, state, description, targetURL string) error {
	base, err := repoPath(repo)
	if err != nil {
		return err
	}
	if !shaPattern.MatchString(sha) {
		return fmt.Errorf("refusing to set status on invalid sha %q", sha)
	}
	body := map[string]string{"state": state, "description": description, "context": StatusContext}
	if targetURL != "" {
		body["target_url"] = targetURL
	}
	_, err = c.do(ctx, http.MethodPost, fmt.Sprintf("%s/statuses/%s", base, sha), body, nil)
	return err
}

var shaPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

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
