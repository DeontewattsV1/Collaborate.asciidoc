package ghcoord

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// Exit codes of the github-check command. They are part of the public
// interface of the reusable GitHub Action; do not renumber.
const (
	ExitPass          = 0 // complete scan, nothing blocking
	ExitBlocked       = 1 // confirmed overlap or required review missing
	ExitError         = 2 // usage, configuration, permission, or API failure
	ExitIndeterminate = 3 // incomplete scan (0 with --allow-indeterminate)
)

var (
	loginPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})(?:\[bot\])?$`)
	refPattern   = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,255}$`)
)

// ExitCode maps an outcome to the process exit code.
func ExitCode(o Outcome, allowIndeterminate bool) int {
	switch o {
	case OutcomePass:
		return ExitPass
	case OutcomeBlocked:
		return ExitBlocked
	case OutcomeIndeterminate:
		if allowIndeterminate {
			return ExitPass
		}
		return ExitIndeterminate
	default:
		return ExitError
	}
}

// ValidateAPIURL accepts https URLs, and http only for loopback hosts (tests
// and local fakes). Credentials, queries, and fragments are rejected so the
// token is never sent somewhere unexpected.
func ValidateAPIURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("--api-url %q is not an absolute URL", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("--api-url must not contain credentials, a query, or a fragment")
	}
	host := u.Hostname()
	loopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return fmt.Errorf("--api-url must use https (http is allowed only for localhost)")
	}
	return nil
}

func validRef(ref string) bool {
	return refPattern.MatchString(ref) && !strings.Contains(ref, "..") && !strings.HasPrefix(ref, "/") && !strings.HasSuffix(ref, "/")
}

// errorReport is printed with --json when the check cannot run, so consumers
// always receive a parseable document with an outcome.
type errorReport struct {
	SchemaVersion string  `json:"schema_version"`
	Outcome       Outcome `json:"outcome"`
	Error         string  `json:"error"`
}

// RunCLI implements `collaborate github-check` and the standalone
// `collaborate-check` binary. It returns the process exit code (see Exit*).
func RunCLI(name string, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if getenv == nil {
		getenv = os.Getenv
	}
	envOr := func(k, fallback string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return fallback
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", getenv("GITHUB_REPOSITORY"), "repository as OWNER/NAME (default $GITHUB_REPOSITORY)")
	pr := fs.Int("pr", 0, "pull request number")
	registryPath := fs.String("registry", ".github/collaborate-agents.json", "agent registry path")
	registryRef := fs.String("registry-ref", "", "read --registry from this branch/ref through the GitHub API instead of the local disk")
	registryOptional := fs.Bool("registry-optional", false, "use an empty registry (no attribution) when the registry file does not exist")
	apiURL := fs.String("api-url", envOr("GITHUB_API_URL", "https://api.github.com"), "GitHub REST API base URL")
	comment := fs.Bool("comment", false, "create or update the sticky coordination comment on the PR")
	commentAuthor := fs.String("comment-author", "github-actions[bot]", "only update an existing sticky comment written by this login (empty: any)")
	summary := fs.String("summary", "", "append the Markdown report to this file (e.g. $GITHUB_STEP_SUMMARY)")
	jsonOut := fs.Bool("json", false, "print the report as JSON (schema "+SchemaVersion+") instead of Markdown")
	output := fs.String("output", "", "append outcome=, exit_code=, blocking_conflicts=, head_sha= lines to this file (e.g. $GITHUB_OUTPUT)")
	maxOpen := fs.Int("max-open", 100, "maximum number of other open PRs to compare (1-1000); more makes the result INDETERMINATE")
	headSHA := fs.String("head-sha", "", "head SHA from the triggering event; a mismatch makes the result INDETERMINATE")
	reconcile := fs.Bool("reconcile", false, "also re-check open PRs that overlap this one and refresh their comment/status")
	setStatus := fs.Bool("set-status", false, "set the "+StatusContext+" commit status on the checked head (needs statuses: write)")
	allowIndeterminate := fs.Bool("allow-indeterminate", false, "exit 0 instead of 3 when the scan is incomplete (the report still says INDETERMINATE)")
	timeout := fs.Duration("timeout", 2*time.Minute, "overall API timeout (1s-30m)")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}

	fail := func(format string, a ...any) int {
		msg := fmt.Sprintf(format, a...)
		fmt.Fprintf(stderr, "%s: %s\n", name, msg)
		fmt.Fprintf(stderr, "::error::%s\n", strings.ReplaceAll(msg, "\n", " "))
		if *jsonOut {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(errorReport{SchemaVersion: SchemaVersion, Outcome: OutcomeError, Error: msg})
		}
		if *output != "" {
			_ = appendFile(*output, fmt.Sprintf("outcome=%s\nexit_code=%d\nblocking_conflicts=0\nhead_sha=\nschema_version=%s\n", OutcomeError, ExitError, SchemaVersion))
		}
		return ExitError
	}

	switch {
	case fs.NArg() > 0:
		return fail("unexpected argument %q", fs.Arg(0))
	case *pr <= 0:
		return fail("--pr is required and must be a positive number")
	case *pr > 1_000_000_000:
		return fail("--pr %d is out of range", *pr)
	case !ValidRepo(*repo):
		return fail("--repo must be OWNER/NAME, got %q", *repo)
	case *maxOpen < 1 || *maxOpen > 1000:
		return fail("--max-open must be between 1 and 1000, got %d", *maxOpen)
	case *timeout < time.Second || *timeout > 30*time.Minute:
		return fail("--timeout must be between 1s and 30m, got %s", *timeout)
	case *commentAuthor != "" && !loginPattern.MatchString(*commentAuthor):
		return fail("--comment-author %q is not a GitHub login", *commentAuthor)
	case *headSHA != "" && !shaPattern.MatchString(strings.ToLower(*headSHA)):
		return fail("--head-sha must be a full 40- or 64-character hex commit SHA")
	case *registryRef != "" && !validRef(*registryRef):
		return fail("--registry-ref %q is not a valid branch or ref name", *registryRef)
	case *registryPath == "":
		return fail("--registry must not be empty")
	}
	if err := ValidateAPIURL(*apiURL); err != nil {
		return fail("%v", err)
	}
	token := getenv("GITHUB_TOKEN")
	if token == "" {
		return fail("GITHUB_TOKEN is not set")
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return fail("GITHUB_TOKEN contains whitespace")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	client := &Client{BaseURL: *apiURL, Token: token}

	var reg *Registry
	var regNote string
	if *registryRef != "" {
		r, found, err := client.FetchRegistry(ctx, *repo, *registryPath, *registryRef)
		switch {
		case err != nil:
			return fail("registry: %v", err)
		case !found && !*registryOptional:
			return fail("registry %s not found at ref %s (pass --registry-optional to run without attribution)", *registryPath, *registryRef)
		case !found:
			reg, regNote = &Registry{Version: 1}, fmt.Sprintf("No `%s` on `%s`; agents are unattributed.", escapeCell(*registryPath), escapeCell(*registryRef))
		default:
			reg = r
		}
	} else {
		r, err := LoadRegistry(*registryPath)
		switch {
		case err != nil && errors.Is(err, os.ErrNotExist) && *registryOptional:
			reg, regNote = &Registry{Version: 1}, fmt.Sprintf("No `%s` found; agents are unattributed.", escapeCell(*registryPath))
		case err != nil:
			return fail("%v", err)
		default:
			reg = r
		}
	}

	res, err := client.Run(ctx, reg, CheckOptions{
		Repo: *repo, Number: *pr, MaxOpen: *maxOpen, ExpectedHeadSHA: strings.ToLower(*headSHA), Reconcile: *reconcile,
	})
	if err != nil {
		if IsPermission(err) {
			return fail("GitHub API denied access (check the token's contents: read and pull-requests: read permissions): %v", err)
		}
		return fail("%v", err)
	}
	rep := res.Report
	if regNote != "" {
		rep.Notes = append(rep.Notes, regNote)
	}
	targetURL := runURL(getenv)

	publish := func(r *Report) (commented, status bool, code int) {
		md := r.Markdown()
		if *comment {
			if err := client.UpsertComment(ctx, *repo, r.PR.Number, md, *commentAuthor); err != nil {
				if IsPermission(err) {
					// Read-only tokens (e.g. fork PRs on pull_request) cannot
					// comment; the job result and step summary still carry it.
					fmt.Fprintf(stderr, "::warning::coordination comment on #%d not posted: token lacks pull-requests: write\n", r.PR.Number)
				} else {
					fmt.Fprintf(stderr, "%s: posting comment on #%d: %v\n", name, r.PR.Number, err)
					return false, false, ExitError
				}
			} else {
				commented = true
			}
		}
		if *setStatus && r.LeaseHeld && r.HeadSHA != "" {
			state, desc := StatusState(r.Outcome, *allowIndeterminate)
			if err := client.SetStatus(ctx, *repo, r.HeadSHA, state, desc, targetURL); err != nil {
				if IsPermission(err) {
					fmt.Fprintf(stderr, "::warning::commit status on #%d not set: token lacks statuses: write\n", r.PR.Number)
				} else {
					fmt.Fprintf(stderr, "%s: setting status on #%d: %v\n", name, r.PR.Number, err)
					return commented, false, ExitError
				}
			} else {
				status = true
			}
		}
		return commented, status, ExitPass
	}

	publishFailed := false
	for _, r := range res.Reconciled {
		c, s, code := publish(r)
		if code != ExitPass {
			publishFailed = true
		}
		rep.Reconciled = append(rep.Reconciled, Reconciled{Number: r.PR.Number, HeadSHA: r.HeadSHA, Outcome: r.Outcome, CommentUpdated: c, StatusSet: s})
	}
	rep.normalize()
	if _, _, code := publish(rep); code != ExitPass {
		publishFailed = true
	}

	md := rep.Markdown()
	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return fail("%v", err)
		}
	} else {
		fmt.Fprint(stdout, md)
	}
	if *summary != "" {
		if err := appendFile(*summary, md); err != nil {
			fmt.Fprintf(stderr, "::warning::writing summary: %v\n", err)
		}
	}
	code := ExitCode(rep.Outcome, *allowIndeterminate)
	if publishFailed {
		code = ExitError
	}
	if *output != "" {
		line := fmt.Sprintf("outcome=%s\nexit_code=%d\nblocking_conflicts=%d\nhead_sha=%s\nschema_version=%s\n",
			rep.Outcome, code, rep.BlockingConflictCount, rep.HeadSHA, SchemaVersion)
		if err := appendFile(*output, line); err != nil {
			fmt.Fprintf(stderr, "::warning::writing outputs: %v\n", err)
		}
	}
	switch rep.Outcome {
	case OutcomeBlocked:
		switch {
		case rep.BlockingConflictCount > 0:
			fmt.Fprintf(stderr, "::error::%d lease conflict(s) with earlier open PRs\n", rep.BlockingConflictCount)
		case rep.ChangesRequested:
			fmt.Fprintln(stderr, "::error::changes requested")
		default:
			fmt.Fprintln(stderr, "::error::independent approval on the current head is required")
		}
	case OutcomeIndeterminate:
		level := "error"
		if *allowIndeterminate {
			level = "warning"
		}
		fmt.Fprintf(stderr, "::%s::coordination scan incomplete (%s); not a verified pass\n", level, strings.Join(rep.Coverage.Gaps, ", "))
	}
	return code
}

// runURL links a commit status to the Actions run, when running in Actions.
func runURL(getenv func(string) string) string {
	server, repo, id := getenv("GITHUB_SERVER_URL"), getenv("GITHUB_REPOSITORY"), getenv("GITHUB_RUN_ID")
	if server == "" || id == "" || !ValidRepo(repo) {
		return ""
	}
	u, err := url.Parse(server)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ""
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return strings.TrimRight(server, "/") + "/" + repo + "/actions/runs/" + id
}

func appendFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(content)
	return errors.Join(werr, f.Close())
}
