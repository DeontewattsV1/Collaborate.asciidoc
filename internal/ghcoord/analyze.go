package ghcoord

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// PR is the coordination view of one pull request.
type PR struct {
	Number      int         `json:"number"`
	Title       string      `json:"title"`
	URL         string      `json:"url"`
	Author      string      `json:"author"`
	Branch      string      `json:"branch"`
	BaseRef     string      `json:"base_ref"`
	HeadSHA     string      `json:"head_sha"`
	Draft       bool        `json:"draft"`
	CreatedAt   time.Time   `json:"created_at"`
	Files       []string    `json:"files"`
	Claims      []string    `json:"claims,omitempty"`
	Attribution Attribution `json:"attribution"`
	// FilesTruncated is set when GitHub's file listing limit was reached, so
	// the lease may be incomplete.
	FilesTruncated bool `json:"files_truncated,omitempty"`
}

// Review is one submitted pull request review.
type Review struct {
	Reviewer    string    `json:"reviewer"`
	State       string    `json:"state"` // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED
	CommitID    string    `json:"commit_id"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// Conflict is an overlap between the leases of two open pull requests.
type Conflict struct {
	Other    PR       `json:"other"`
	Paths    []string `json:"paths"`
	Holder   int      `json:"holder"`   // PR number that holds the lease (opened first)
	Blocking bool     `json:"blocking"` // true when the current PR must yield
}

// ReviewVerdict is the latest effective review from one reviewer.
type ReviewVerdict struct {
	Reviewer    string      `json:"reviewer"`
	Attribution Attribution `json:"attribution"`
	State       string      `json:"state"`
	Current     bool        `json:"current"`     // review is on the head commit
	Independent bool        `json:"independent"` // reviewer is not the author's identity
}

// SchemaVersion identifies the JSON report format. Consumers should reject
// versions they do not understand; additive fields keep the same version.
const SchemaVersion = "collaborate.check/v1"

// Outcome is the verdict of one coordination check. Only PASS means a
// complete scan found nothing blocking; INDETERMINATE never counts as a pass.
type Outcome string

const (
	// OutcomePass: a complete scan found no blocking coordination issue.
	OutcomePass Outcome = "PASS"
	// OutcomeBlocked: a confirmed overlap or required policy violation exists.
	OutcomeBlocked Outcome = "BLOCKED"
	// OutcomeIndeterminate: coverage was incomplete, evidence was unavailable,
	// or the pull request changed while it was being checked.
	OutcomeIndeterminate Outcome = "INDETERMINATE"
	// OutcomeError: configuration, permission, or service failure.
	OutcomeError Outcome = "ERROR"
)

// Coverage records how much of the evidence the check actually saw.
type Coverage struct {
	Complete bool `json:"complete"`
	// OtherOpenCompared is how many other open PRs were compared.
	OtherOpenCompared int `json:"other_open_compared"`
	// OtherOpenListed is how many other open PRs the listing returned; it is a
	// lower bound when the listing itself was truncated.
	OtherOpenListed int `json:"other_open_listed"`
	// Gaps are stable machine-readable reasons the scan is incomplete, e.g.
	// "open_pr_limit", "open_pr_listing_truncated", "files_truncated:#12",
	// "reviews_truncated", "stale_event_head", "pr_changed_during_scan".
	Gaps []string `json:"gaps"`
}

// Reconciled summarizes a re-check of another open PR whose result may have
// changed because of the checked PR (for example, it closed or narrowed).
type Reconciled struct {
	Number         int     `json:"number"`
	HeadSHA        string  `json:"head_sha"`
	Outcome        Outcome `json:"outcome"`
	CommentUpdated bool    `json:"comment_updated"`
	StatusSet      bool    `json:"status_set"`
}

// Report is the coordination result for one pull request.
type Report struct {
	SchemaVersion string  `json:"schema_version"`
	Outcome       Outcome `json:"outcome"`
	Repository    string  `json:"repository,omitempty"`
	// HeadSHA is the head commit the verdict applies to, as GitHub reported it
	// when the scan finished. Enforcement must be reported against this SHA.
	HeadSHA string `json:"head_sha"`
	// PRState is "open", "closed", or "merged".
	PRState string `json:"pr_state"`
	// LeaseHeld is false once the PR is closed or merged.
	LeaseHeld           bool            `json:"lease_held"`
	Coverage            Coverage        `json:"coverage"`
	PR                  PR              `json:"pr"`
	Open                []PR            `json:"open"`
	Conflicts           []Conflict      `json:"conflicts"`
	Reviews             []ReviewVerdict `json:"reviews"`
	IndependentApproval bool            `json:"independent_approval"`
	// HumanApproval is an approval on the current head from a login listed in
	// the registry's humans (other than the author). Agent approvals never
	// count here, even when they are independent.
	HumanApproval         bool         `json:"human_approval"`
	ChangesRequested      bool         `json:"changes_requested"`
	EnforceIndependent    bool         `json:"enforce_independent_review"`
	Notes                 []string     `json:"notes"`
	BlockingConflictCount int          `json:"blocking_conflict_count"`
	Reconciled            []Reconciled `json:"reconciled"`
}

// Failed reports whether a confirmed coordination failure exists. A PR that
// no longer holds a lease (closed or merged) never fails.
func (r *Report) Failed() bool {
	if !r.LeaseHeld {
		return false
	}
	return r.BlockingConflictCount > 0 || (r.EnforceIndependent && (!r.IndependentApproval || r.ChangesRequested))
}

// AddGap records an incomplete-coverage reason once.
func (r *Report) AddGap(code string) {
	for _, g := range r.Coverage.Gaps {
		if g == code {
			return
		}
	}
	r.Coverage.Gaps = append(r.Coverage.Gaps, code)
	sort.Strings(r.Coverage.Gaps)
	r.Coverage.Complete = false
}

// Decide sets Outcome from the report's evidence. Confirmed failures win over
// incomplete coverage; incomplete coverage never becomes PASS.
func (r *Report) Decide() {
	switch {
	case !r.LeaseHeld:
		r.Outcome = OutcomePass
	case r.Failed():
		r.Outcome = OutcomeBlocked
	case !r.Coverage.Complete:
		r.Outcome = OutcomeIndeterminate
	default:
		r.Outcome = OutcomePass
	}
}

// normalize makes JSON output deterministic: empty lists are [] (not null)
// and every list has a stable order.
func (r *Report) normalize() {
	if r.Open == nil {
		r.Open = []PR{}
	}
	if r.Conflicts == nil {
		r.Conflicts = []Conflict{}
	}
	if r.Reviews == nil {
		r.Reviews = []ReviewVerdict{}
	}
	if r.Notes == nil {
		r.Notes = []string{}
	}
	if r.Coverage.Gaps == nil {
		r.Coverage.Gaps = []string{}
	}
	if r.Reconciled == nil {
		r.Reconciled = []Reconciled{}
	}
	sort.Slice(r.Reconciled, func(i, j int) bool { return r.Reconciled[i].Number < r.Reconciled[j].Number })
	normPR := func(p *PR) {
		if p.Files == nil {
			p.Files = []string{}
		}
		sort.Strings(p.Files)
	}
	normPR(&r.PR)
	for i := range r.Open {
		normPR(&r.Open[i])
	}
	for i := range r.Conflicts {
		normPR(&r.Conflicts[i].Other)
	}
}

var claimPattern = regexp.MustCompile(`(?im)^\s*collaborate-claim:\s*(\S+)\s*$`)

// ParseClaims extracts "Collaborate-Claim: PATH" declarations from a pull
// request body. A claim reserves a file or directory beyond the changed files,
// letting an agent announce intent before pushing. Invalid paths are returned
// separately so they can be reported.
func ParseClaims(body string) (claims, invalid []string) {
	seen := map[string]bool{}
	for _, m := range claimPattern.FindAllStringSubmatch(body, -1) {
		p, ok := normalizePath(m[1])
		if !ok {
			invalid = append(invalid, m[1])
			continue
		}
		if !seen[p] {
			seen[p] = true
			claims = append(claims, p)
		}
	}
	sort.Strings(claims)
	return claims, invalid
}

func normalizePath(p string) (string, bool) {
	p = strings.TrimSpace(strings.Trim(p, "`"))
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return "", false
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", false
	}
	return c, true
}

// overlaps reports whether two repository paths overlap hierarchically: they
// are equal, or one is an ancestor directory of the other.
func overlaps(a, b string) bool {
	return a == b || strings.HasPrefix(b, a+"/") || strings.HasPrefix(a, b+"/")
}

func lease(p PR) []string {
	out := append([]string{}, p.Files...)
	return append(out, p.Claims...)
}

func overlapPaths(a, b PR) []string {
	set := map[string]bool{}
	for _, x := range lease(a) {
		for _, y := range lease(b) {
			if overlaps(x, y) {
				if len(x) >= len(y) {
					set[x] = true
				} else {
					set[y] = true
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// opensFirst reports whether a was opened before b (ties go to the lower number).
func opensFirst(a, b PR) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.Number < b.Number
}

// Analyze computes lease conflicts and review independence for current,
// against every other open pull request targeting the same base branch.
func Analyze(reg *Registry, current PR, open []PR, reviews []Review) *Report {
	rep := &Report{
		SchemaVersion: SchemaVersion, PR: current, HeadSHA: current.HeadSHA, PRState: "open", LeaseHeld: true,
		Coverage: Coverage{Complete: true}, EnforceIndependent: reg.EnforceIndependentReview,
	}
	for _, o := range open {
		if o.Number == current.Number {
			continue
		}
		rep.Open = append(rep.Open, o)
		if o.BaseRef != current.BaseRef {
			continue
		}
		paths := overlapPaths(current, o)
		if len(paths) == 0 {
			continue
		}
		holder := o.Number
		if opensFirst(current, o) {
			holder = current.Number
		}
		c := Conflict{Other: o, Paths: paths, Holder: holder, Blocking: holder != current.Number}
		if c.Blocking {
			rep.BlockingConflictCount++
		}
		rep.Conflicts = append(rep.Conflicts, c)
	}
	sort.Slice(rep.Open, func(i, j int) bool { return rep.Open[i].Number < rep.Open[j].Number })
	sort.Slice(rep.Conflicts, func(i, j int) bool { return rep.Conflicts[i].Other.Number < rep.Conflicts[j].Other.Number })
	rep.Coverage.OtherOpenCompared = len(rep.Open)
	rep.Coverage.OtherOpenListed = len(rep.Open)
	if current.FilesTruncated {
		rep.AddGap(fmt.Sprintf("files_truncated:#%d", current.Number))
		rep.Notes = append(rep.Notes, "GitHub's changed-file listing limit was reached; this PR's lease may be incomplete.")
	}
	for _, o := range rep.Open {
		if o.FilesTruncated && o.BaseRef == current.BaseRef {
			rep.AddGap(fmt.Sprintf("files_truncated:#%d", o.Number))
			rep.Notes = append(rep.Notes, fmt.Sprintf("PR #%d's changed-file listing was truncated; overlaps with it may be missed.", o.Number))
		}
	}
	rep.Reviews = effectiveReviews(reg, current, reviews)
	for _, v := range rep.Reviews {
		switch v.State {
		case "APPROVED":
			if v.Current && v.Independent {
				rep.IndependentApproval = true
				if v.Attribution.Kind == "human" {
					rep.HumanApproval = true
				}
			}
		case "CHANGES_REQUESTED":
			rep.ChangesRequested = true
		}
	}
	rep.Decide()
	rep.normalize()
	return rep
}

// effectiveReviews keeps each reviewer's latest decisive review, matching
// GitHub's semantics: a COMMENTED review does not replace an earlier approval
// or change request, and DISMISSED clears it.
func effectiveReviews(reg *Registry, current PR, reviews []Review) []ReviewVerdict {
	sorted := append([]Review{}, reviews...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].SubmittedAt.Before(sorted[j].SubmittedAt) })
	latest := map[string]Review{}
	order := []string{}
	for _, r := range sorted {
		k := strings.ToLower(r.Reviewer)
		switch r.State {
		case "APPROVED", "CHANGES_REQUESTED", "DISMISSED":
		default:
			if _, ok := latest[k]; ok {
				continue
			}
		}
		if _, ok := latest[k]; !ok {
			order = append(order, k)
		}
		latest[k] = r
	}
	var out []ReviewVerdict
	for _, k := range order {
		r := latest[k]
		if r.State == "DISMISSED" {
			continue
		}
		a := reg.AttributeReviewer(r.Reviewer)
		independent := !strings.EqualFold(r.Reviewer, current.Author) &&
			(current.Attribution.AgentID == "" || a.AgentID != current.Attribution.AgentID)
		out = append(out, ReviewVerdict{
			Reviewer:    r.Reviewer,
			Attribution: a,
			State:       r.State,
			Current:     current.HeadSHA == "" || r.CommitID == current.HeadSHA,
			Independent: independent,
		})
	}
	return out
}

// CommentMarker identifies the sticky coordination comment.
const CommentMarker = "<!-- collaborate-coordination -->"

func describeAttribution(a Attribution) string {
	switch a.Source {
	case "login":
		return fmt.Sprintf("`%s` (%s), verified GitHub login `%s`", a.AgentID, a.Platform, a.Evidence)
	case "branch":
		return fmt.Sprintf("`%s` (%s), from branch prefix `%s` (self-reported)", a.AgentID, a.Platform, a.Evidence)
	case "trailer":
		return fmt.Sprintf("`%s` (%s), from Co-Authored-By `%s` (self-reported)", a.AgentID, a.Platform, a.Evidence)
	case "human":
		return fmt.Sprintf("human `%s`", a.AgentID)
	default:
		return fmt.Sprintf("unattributed (author `%s` is not in `.github/collaborate-agents.json`)", a.Evidence)
	}
}

func summarizePaths(paths []string, max int) string {
	if len(paths) == 0 {
		return "—"
	}
	quoted := make([]string, 0, max)
	for i, p := range paths {
		if i == max {
			quoted = append(quoted, fmt.Sprintf("+%d more", len(paths)-max))
			break
		}
		quoted = append(quoted, "`"+escapeCell(p)+"`")
	}
	return strings.Join(quoted, ", ")
}

func escapeCell(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ", "`", "'").Replace(s)
}

// ProjectURL is linked from the comment footer.
const ProjectURL = "https://github.com/DeontewattsV1/Collaborate.asciidoc"

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	if sha == "" {
		return "unknown"
	}
	return sha
}

func describeGap(g string) string {
	switch {
	case g == "open_pr_limit":
		return "more open PRs exist than --max-open allows"
	case g == "open_pr_listing_truncated":
		return "the open-PR listing hit the page limit"
	case g == "reviews_truncated":
		return "the review listing was truncated"
	case g == "stale_event_head":
		return "the triggering event's head is no longer the PR head"
	case g == "pr_changed_during_scan":
		return "the PR changed while it was being checked"
	case strings.HasPrefix(g, "files_truncated:"):
		return "changed files truncated for " + strings.TrimPrefix(g, "files_truncated:")
	default:
		return g
	}
}

func attributionEvidence(a Attribution) string {
	switch a.Source {
	case "login", "human":
		return "verified GitHub login"
	case "branch", "trailer":
		return "self-reported (" + a.Source + "), not verified"
	default:
		return "unattributed"
	}
}

// Markdown renders the report as the sticky pull request comment. The output
// doubles as shared context: every agent reading the pull request sees who is
// working on what across all open pull requests.
func (r *Report) Markdown() string {
	var b strings.Builder
	b.WriteString(CommentMarker + "\n")
	outcome := r.Outcome
	if outcome == "" {
		outcome = OutcomePass
	}
	fmt.Fprintf(&b, "## Collaborate coordination: %s\n\n", outcome)
	var status string
	switch {
	case !r.LeaseHeld:
		status = fmt.Sprintf("ℹ️ This PR is %s; it no longer holds a lease. Other PRs that overlapped it are re-checked.", r.PRState)
	case r.BlockingConflictCount > 0:
		status = fmt.Sprintf("❌ %d lease conflict(s): an earlier open PR already holds overlapping paths. Coordinate there, narrow this PR, or wait for it to merge or close.", r.BlockingConflictCount)
	case outcome == OutcomeBlocked && r.ChangesRequested:
		status = "❌ Changes requested."
	case outcome == OutcomeBlocked:
		status = "❌ Independent approval on the current head is required."
	case outcome == OutcomeIndeterminate:
		status = "⚠️ Scan incomplete, so this is not a verified pass. See scan coverage below."
	case len(r.Conflicts) > 0:
		status = "✅ No blocking lease conflicts. ⚠️ This PR holds the lease on paths that later PRs also change; they must yield to it."
	default:
		status = "✅ No lease conflicts."
	}
	fmt.Fprintf(&b, "%s\n\n", status)
	if r.BlockingConflictCount > 0 && r.LeaseHeld {
		b.WriteString("**Recommended resolution:** narrow this PR to unclaimed files, or request a handoff from the lease holder.\n\n")
	}
	fmt.Fprintf(&b, "- **Agent:** %s\n", describeAttribution(r.PR.Attribution))
	fmt.Fprintf(&b, "- **Lease:** %d changed file(s)", len(r.PR.Files))
	if len(r.PR.Claims) > 0 {
		fmt.Fprintf(&b, " plus declared claims %s", summarizePaths(r.PR.Claims, 10))
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "- **Checked head:** `%s`\n", escapeCell(shortSHA(r.HeadSHA)))
	cov := fmt.Sprintf("%d/%d other open PRs compared", r.Coverage.OtherOpenCompared, r.Coverage.OtherOpenListed)
	if r.Coverage.Complete {
		cov += " (complete)"
	} else {
		reasons := make([]string, 0, len(r.Coverage.Gaps))
		for _, g := range r.Coverage.Gaps {
			reasons = append(reasons, escapeCell(describeGap(g)))
		}
		cov += " (incomplete: " + strings.Join(reasons, "; ") + ")"
	}
	fmt.Fprintf(&b, "- **Scan coverage:** %s\n", cov)

	switch {
	case r.ChangesRequested:
		b.WriteString("- **Independent review:** ❌ changes requested\n")
	case r.IndependentApproval:
		b.WriteString("- **Independent review:** ✅ approved on the current head by an identity other than the author\n")
	default:
		b.WriteString("- **Independent review:** ⏳ waiting for approval on the current head from a different agent or a human other than the author")
		if r.EnforceIndependent {
			b.WriteString(" (required)")
		}
		b.WriteString("\n")
	}

	b.WriteString("\n| Evidence | Status |\n|---|---|\n")
	fmt.Fprintf(&b, "| GitHub account attribution | %s |\n", attributionEvidence(r.PR.Attribution))
	b.WriteString("| Underlying AI model identity | not verified (GitHub cannot prove which model wrote a change) |\n")
	if r.HumanApproval {
		b.WriteString("| Independent human approval | ✅ on the current head |\n")
	} else {
		b.WriteString("| Independent human approval | ⏳ pending |\n")
	}

	if len(r.Conflicts) > 0 {
		b.WriteString("\n### Overlapping work\n\n| PR | Agent | Overlapping paths | Lease holder |\n|---|---|---|---|\n")
		for _, c := range r.Conflicts {
			fmt.Fprintf(&b, "| #%d %s | `%s` | %s | #%d |\n", c.Other.Number, escapeCell(c.Other.Title), c.Other.Attribution.Label(), summarizePaths(c.Paths, 8), c.Holder)
		}
	}

	if len(r.Reviews) > 0 {
		b.WriteString("\n### Reviews\n\n| Reviewer | Identity | Verdict | On head | Independent |\n|---|---|---|---|---|\n")
		for _, v := range r.Reviews {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n", escapeCell(v.Reviewer), escapeCell(v.Attribution.Label()), v.State, yesNo(v.Current), yesNo(v.Independent))
		}
	}

	if len(r.Open) > 0 {
		b.WriteString("\n### Other open agent work\n\n| PR | Agent | Base | Files | Draft |\n|---|---|---|---|---|\n")
		for _, o := range r.Open {
			fmt.Fprintf(&b, "| #%d %s | `%s` | `%s` | %s | %s |\n", o.Number, escapeCell(o.Title), o.Attribution.Label(), escapeCell(o.BaseRef), summarizePaths(lease(o), 4), yesNo(o.Draft))
		}
	}
	for _, n := range r.Notes {
		fmt.Fprintf(&b, "\n> %s\n", n)
	}
	b.WriteString("\n<sub>Leases are the paths each open PR changes, plus any `Collaborate-Claim: PATH` lines in its description; the earliest-opened PR holds an overlapping lease. A path overlap is not necessarily a Git merge conflict. Agents are identified from `.github/collaborate-agents.json` on the base branch. Only GitHub-login attribution is verified; branch-prefix and trailer attribution are self-reported.</sub>\n")
	fmt.Fprintf(&b, "\n<sub>Powered by [Collaborate](%s) · open-source agent coordination</sub>\n", ProjectURL)
	return b.String()
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
