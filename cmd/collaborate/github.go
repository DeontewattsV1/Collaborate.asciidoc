package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"collaborate/internal/ghcoord"
)

// runGitHubCheck implements `collaborate github-check`. It returns the process
// exit code: 0 pass, 1 coordination failure, 2 usage or API error.
func runGitHubCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("github-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", os.Getenv("GITHUB_REPOSITORY"), "repository as OWNER/NAME (default $GITHUB_REPOSITORY)")
	pr := fs.Int("pr", 0, "pull request number")
	registryPath := fs.String("registry", ".github/collaborate-agents.json", "agent registry read from the trusted base branch")
	apiURL := fs.String("api-url", envOr("GITHUB_API_URL", "https://api.github.com"), "GitHub REST API base URL")
	comment := fs.Bool("comment", false, "create or update the sticky coordination comment on the PR")
	commentAuthor := fs.String("comment-author", "github-actions[bot]", "only update an existing sticky comment written by this login (empty: any)")
	summary := fs.String("summary", "", "append the Markdown report to this file (e.g. $GITHUB_STEP_SUMMARY)")
	jsonOut := fs.Bool("json", false, "print the report as JSON instead of Markdown")
	maxOpen := fs.Int("max-open", 50, "maximum number of other open PRs to compare against")
	timeout := fs.Duration("timeout", 2*time.Minute, "overall API timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *pr <= 0 {
		fmt.Fprintln(stderr, "collaborate github-check: --pr is required")
		return 2
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		fmt.Fprintln(stderr, "collaborate github-check: GITHUB_TOKEN is not set")
		return 2
	}
	reg, err := ghcoord.LoadRegistry(*registryPath)
	if err != nil {
		fmt.Fprintln(stderr, "collaborate github-check:", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	client := &ghcoord.Client{BaseURL: *apiURL, Token: token}
	rep, err := client.Check(ctx, reg, *repo, *pr, *maxOpen)
	if err != nil {
		fmt.Fprintln(stderr, "collaborate github-check:", err)
		return 2
	}
	md := rep.Markdown()
	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintln(stderr, "collaborate github-check:", err)
			return 2
		}
	} else {
		fmt.Fprint(stdout, md)
	}
	if *summary != "" {
		if err := appendFile(*summary, md); err != nil {
			fmt.Fprintln(stderr, "collaborate github-check: writing summary:", err)
		}
	}
	if *comment {
		if err := client.UpsertComment(ctx, *repo, *pr, md, *commentAuthor); err != nil {
			if ghcoord.IsPermission(err) {
				// Read-only tokens (e.g. reviews on fork PRs) cannot comment;
				// the job result and step summary still carry the report.
				fmt.Fprintln(stderr, "::warning::coordination comment not posted: token lacks pull-requests: write")
			} else {
				fmt.Fprintln(stderr, "collaborate github-check: posting comment:", err)
				return 2
			}
		}
	}
	if rep.Failed() {
		switch {
		case rep.BlockingConflictCount > 0:
			fmt.Fprintf(stderr, "::error::%d lease conflict(s) with earlier open PRs\n", rep.BlockingConflictCount)
		case rep.ChangesRequested:
			fmt.Fprintln(stderr, "::error::changes requested")
		default:
			fmt.Fprintln(stderr, "::error::independent approval on the current head is required")
		}
		return 1
	}
	return 0
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func appendFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(content)
	return errors.Join(werr, f.Close())
}
