package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"collaborate/internal/agentregistry"
)

type pullRequestEvent struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest struct {
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
	} `json:"pull_request"`
}

func main() {
	registryPath := flag.String("registry", ".github/agent-registry.json", "trusted registry file path")
	eventPath := flag.String("event", os.Getenv("GITHUB_EVENT_PATH"), "GitHub event JSON path")
	repository := flag.String("repository", os.Getenv("GITHUB_REPOSITORY"), "owner/repository")
	apiURL := flag.String("api-url", os.Getenv("GITHUB_API_URL"), "GitHub API base URL")
	flag.Parse()
	if err := run(*registryPath, *eventPath, os.Getenv("GITHUB_TOKEN"), *repository, *apiURL, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "agent-registry:", err)
		os.Exit(1)
	}
}

func run(registryPath, eventPath, token, repository, apiURL string, out io.Writer) error {
	registry, err := agentregistry.Load(registryPath)
	if err != nil {
		return err
	}
	if eventPath == "" {
		return errors.New("GITHUB_EVENT_PATH is required")
	}
	eventData, err := os.ReadFile(eventPath)
	if err != nil {
		return fmt.Errorf("read GitHub event: %w", err)
	}
	var event pullRequestEvent
	if err := json.Unmarshal(eventData, &event); err != nil {
		return fmt.Errorf("decode GitHub pull_request_target event: %w", err)
	}
	pr := event.PullRequest
	if pr.Number == 0 {
		pr.Number = event.Number
	}
	if pr.Number <= 0 || pr.Head.Ref == "" || pr.Base.Ref != "main" || pr.User.ID <= 0 || pr.User.Login == "" {
		return errors.New("event is missing a main-target pull request number, head branch, or author identity")
	}
	if err := validGitBranch(pr.Head.Ref); err != nil {
		return err
	}
	accountType := "user"
	if strings.EqualFold(pr.User.Type, "bot") {
		accountType = "bot"
	}
	author := agentregistry.PullRequestIdentity{AccountType: accountType, Login: pr.User.Login, GitHubUserID: pr.User.ID}
	resolved, err := registry.ResolvePullRequestAgent(author, pr.Head.Ref)
	if err != nil {
		return fmt.Errorf("PR #%d author/branch is not authorized: %w", pr.Number, err)
	}
	api := &agentregistry.GitHubClient{BaseURL: apiURL, Repository: repository, Token: token}
	currentFiles, err := api.PullRequestFiles(pr.Number)
	if err != nil {
		return err
	}
	pulls, err := api.OtherOpenPullRequests(pr.Number, pr.Base.Ref)
	if err != nil {
		return err
	}
	otherFiles := make(map[int][]string, len(pulls))
	urls := make(map[int]string, len(pulls))
	registeredOpenPRs := 0
	for _, p := range pulls {
		if _, err := registry.ResolvePullRequestAgent(p.Author, p.Head); err != nil {
			continue
		}
		paths, err := api.PullRequestFiles(p.Number)
		if err != nil {
			return err
		}
		otherFiles[p.Number] = paths
		urls[p.Number] = p.URL
		registeredOpenPRs++
	}
	conflicts := agentregistry.FindFileConflicts(currentFiles, otherFiles, urls)
	if len(conflicts) > 0 {
		fmt.Fprintf(out, "PR #%d is registered to agent %q but has overlapping open-PR file claims:\n", pr.Number, resolved.ID)
		for _, conflict := range conflicts {
			if conflict.URL != "" {
				fmt.Fprintf(out, "- %q overlaps PR #%d (%s)\n", conflict.Path, conflict.PR, conflict.URL)
			} else {
				fmt.Fprintf(out, "- %q overlaps PR #%d\n", conflict.Path, conflict.PR)
			}
		}
		return errors.New("resolve overlapping open-PR file claims before merge")
	}
	fmt.Fprintf(out, "PR #%d authorized as %q; checked %d changed paths against %d registered open PR claims with no overlaps (ignored %d unregistered PRs).\n", pr.Number, resolved.ID, len(currentFiles), registeredOpenPRs, len(pulls)-registeredOpenPRs)
	return nil
}

func validGitBranch(branch string) error {
	cmd := exec.Command("git", "check-ref-format", "--branch", branch)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("invalid Git branch ref %q: %s", branch, strings.TrimSpace(string(output)))
	}
	return nil
}
