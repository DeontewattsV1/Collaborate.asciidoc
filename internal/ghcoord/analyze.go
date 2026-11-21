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

// Report is the coordination result for one pull request.
type Report struct {
	PR                    PR              `json:"pr"`
	Open                  []PR            `json:"open"`
	Conflicts             []Conflict      `json:"conflicts"`
	Reviews               []ReviewVerdict `json:"reviews"`
	IndependentApproval   bool            `json:"independent_approval"`
	ChangesRequested      bool            `json:"changes_requested"`
	EnforceIndependent    bool            `json:"enforce_independent_review"`
	Notes                 []string        `json:"notes,omitempty"`
	SessionMembers        []SessionMember `json:"session_members,omitempty"`
	BlockingConflictCount int             `json:"blocking_conflict_count"`
}

// Failed reports whether the coordination check should fail.
func (r *Report) Failed() bool {
	return r.BlockingConflictCount > 0 || (r.EnforceIndependent && (!r.IndependentApproval || r.ChangesRequested))
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
	rep := &Report{PR: current, EnforceIndependent: reg.EnforceIndependentReview}
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
	if current.FilesTruncated {
		rep.Notes = append(rep.Notes, "GitHub's changed-file listing limit was reached; this PR's lease may be incomplete.")
	}
	rep.Reviews = effectiveReviews(reg, current, reviews)
	for _, v := range rep.Reviews {
		switch v.State {
		case "APPROVED":
			if v.Current && v.Independent {
				rep.IndependentApproval = true
			}
		case "CHANGES_REQUESTED":
			rep.ChangesRequested = true
		}
	}
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

// Markdown renders the report as the sticky pull request comment. The output
// doubles as shared context: every agent reading the pull request sees who is
// working on what across all open pull requests.
func (r *Report) Markdown() string {
	var b strings.Builder
	b.WriteString(CommentMarker + "\n")
	b.WriteString("## Collaborate coordination\n\n")
	status := "✅ No lease conflicts."
	if r.BlockingConflictCount > 0 {
		status = fmt.Sprintf("❌ %d lease conflict(s): an earlier open PR already holds overlapping paths. Coordinate there, narrow this PR, or wait for it to merge or close.", r.BlockingConflictCount)
	} else if len(r.Conflicts) > 0 {
		status = "⚠️ This PR holds the lease on paths that later PRs also change; they must yield to it."
	}
	fmt.Fprintf(&b, "%s\n\n", status)
	fmt.Fprintf(&b, "- **Agent:** %s\n", describeAttribution(r.PR.Attribution))
	fmt.Fprintf(&b, "- **Lease:** %d changed file(s)", len(r.PR.Files))
	if len(r.PR.Claims) > 0 {
		fmt.Fprintf(&b, " plus declared claims %s", summarizePaths(r.PR.Claims, 10))
	}
	b.WriteString("\n")

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

	if len(r.SessionMembers) > 0 {
		b.WriteString("\n### Participants in this PR session\n\n| GitHub login | Self-reported agent/role | Registry attribution |\n|---|---|---|\n")
		for _, m := range r.SessionMembers {
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", escapeCell(m.Login), escapeCell(m.Label), escapeCell(m.Attribution.Label()))
		}
		b.WriteString("\nParticipation is voluntary and display-only. GitHub authenticates the commenting account, not its claimed model/provider.\n")
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
	b.WriteString("\n<sub>Leases are the paths each open PR changes, plus any `Collaborate-Claim: PATH` lines in its description; the earliest-opened PR holds an overlapping lease. Agents are identified from `.github/collaborate-agents.json` on the base branch. Only GitHub-login attribution is verified; branch-prefix, trailer, and session-role attribution are self-reported.</sub>\n")
	return b.String()
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
