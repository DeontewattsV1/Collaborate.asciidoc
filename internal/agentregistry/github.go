package agentregistry

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const githubPageSize = 100
const maxGitHubPages = 30
const maxGitHubResponse = 16 << 20

type GitHubClient struct {
	BaseURL    string
	Repository string
	Token      string
	HTTPClient *http.Client
}

type PullRequest struct {
	Number int
	URL    string
	Head   string
	Base   string
	Author PullRequestIdentity
}

type apiPull struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	User    struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
		Type  string `json:"type"`
	} `json:"user"`
	Head struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

type apiFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
}

func (c *GitHubClient) validate() error {
	if c == nil || strings.TrimSpace(c.Token) == "" {
		return fmt.Errorf("GitHub API token is required")
	}
	if c.BaseURL == "" {
		c.BaseURL = "https://api.github.com"
	}
	parsed, err := url.Parse(c.BaseURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1"))) {
		return fmt.Errorf("GitHub API URL must be HTTPS (HTTP is allowed only for loopback tests)")
	}
	if strings.TrimSpace(c.Repository) == "" || strings.Count(c.Repository, "/") != 1 {
		return fmt.Errorf("GITHUB_REPOSITORY must be owner/name")
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return nil
}

func (c *GitHubClient) get(endpoint string, out any) error {
	if err := c.validate(); err != nil {
		return err
	}
	u := strings.TrimRight(c.BaseURL, "/") + endpoint
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "collaborate-agent-registry-check")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub API request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxGitHubResponse+1))
	if err != nil {
		return err
	}
	if len(body) > maxGitHubResponse {
		return fmt.Errorf("GitHub API response exceeded 16 MiB")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub API %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode GitHub API response: %w", err)
	}
	return nil
}

func (c *GitHubClient) repoPath(suffix string) string {
	parts := strings.Split(c.Repository, "/")
	return "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + suffix
}

func (c *GitHubClient) listFiles(prNumber int) ([]string, error) {
	if prNumber <= 0 {
		return nil, fmt.Errorf("invalid pull request number")
	}
	var paths []string
	for page := 1; page <= maxGitHubPages; page++ {
		var files []apiFile
		endpoint := c.repoPath(fmt.Sprintf("/pulls/%d/files?per_page=%d&page=%d", prNumber, githubPageSize, page))
		if err := c.get(endpoint, &files); err != nil {
			return nil, fmt.Errorf("list files for PR #%d: %w", prNumber, err)
		}
		for _, f := range files {
			if !validRepositoryPath(f.Filename) {
				return nil, fmt.Errorf("GitHub returned an invalid repository path for PR #%d", prNumber)
			}
			paths = append(paths, f.Filename)
			if f.PreviousFilename != "" {
				if !validRepositoryPath(f.PreviousFilename) {
					return nil, fmt.Errorf("GitHub returned an invalid previous filename for PR #%d", prNumber)
				}
				paths = append(paths, f.PreviousFilename)
			}
		}
		if len(files) < githubPageSize {
			return uniqueSorted(paths), nil
		}
	}
	return nil, fmt.Errorf("PR #%d changed-file listing exceeded %d pages", prNumber, maxGitHubPages)
}

// PullRequestFiles fetches all current paths, including both sides of renames.
func (c *GitHubClient) PullRequestFiles(number int) ([]string, error) {
	return c.listFiles(number)
}

// OtherOpenPullRequests lists open PRs targeting the same base branch except
// excludeNumber. Call PullRequestFiles only after a PR resolves to an active
// registry agent. All API errors are fatal.
func (c *GitHubClient) OtherOpenPullRequests(excludeNumber int, baseBranch string) ([]PullRequest, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	var pulls []PullRequest
	for page := 1; page <= maxGitHubPages; page++ {
		var rows []apiPull
		endpoint := c.repoPath(fmt.Sprintf("/pulls?state=open&per_page=%d&page=%d", githubPageSize, page))
		if err := c.get(endpoint, &rows); err != nil {
			return nil, fmt.Errorf("list open pull requests: %w", err)
		}
		for _, row := range rows {
			if row.Number == excludeNumber || row.Base.Ref != baseBranch {
				continue
			}
			accountType := "user"
			if strings.EqualFold(row.User.Type, "bot") {
				accountType = "bot"
			}
			pulls = append(pulls, PullRequest{Number: row.Number, URL: row.HTMLURL, Head: row.Head.Ref, Base: row.Base.Ref, Author: PullRequestIdentity{AccountType: accountType, Login: row.User.Login, GitHubUserID: row.User.ID}})
		}
		if len(rows) < githubPageSize {
			break
		}
		if page == maxGitHubPages {
			return nil, fmt.Errorf("open pull request listing exceeded %d pages", maxGitHubPages)
		}
	}
	return pulls, nil
}

func validRepositoryPath(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00") {
		return false
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func uniqueSorted(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	sort.Strings(out)
	return out
}
