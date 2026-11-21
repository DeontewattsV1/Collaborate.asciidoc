// Package ghcoord coordinates AI agents that collaborate through GitHub
// itself. Any platform that can push a branch, open a pull request, or submit
// a review (GitHub Apps, OAuth apps, or assistants acting under a user's
// account) participates without platform-specific integration: each open pull
// request is treated as its agent's lease over the paths it changes, overlap
// between open pull requests is a lease conflict, and approval must come from
// an identity independent of the author.
package ghcoord

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Agent describes one AI platform or automation identity. Logins are GitHub
// account logins (for GitHub Apps, the "<slug>[bot]" form). BranchPrefixes and
// TrailerEmails identify work the platform performs under a user's account.
type Agent struct {
	ID             string   `json:"id"`
	Platform       string   `json:"platform"`
	Kind           string   `json:"kind,omitempty"` // "ai" (default) or "automation"
	Logins         []string `json:"logins,omitempty"`
	BranchPrefixes []string `json:"branch_prefixes,omitempty"`
	TrailerEmails  []string `json:"trailer_emails,omitempty"`
}

// Registry is the operator-owned identity map. It must be read from the
// trusted base branch, never from a pull request's head.
type Registry struct {
	Version                  int      `json:"version"`
	Humans                   []string `json:"humans,omitempty"`
	EnforceIndependentReview bool     `json:"enforce_independent_review"`
	Agents                   []Agent  `json:"agents"`
}

var agentIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// LoadRegistry reads and validates a registry file.
func LoadRegistry(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseRegistry(data)
}

// ParseRegistry decodes and validates registry JSON.
func ParseRegistry(data []byte) (*Registry, error) {
	var r Registry
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}
	if r.Version != 1 {
		return nil, fmt.Errorf("registry: unsupported version %d (want 1)", r.Version)
	}
	ids := map[string]bool{}
	logins := map[string]string{}
	prefixes := map[string]string{}
	emails := map[string]string{}
	for _, h := range r.Humans {
		logins[strings.ToLower(h)] = "human"
	}
	for i := range r.Agents {
		a := &r.Agents[i]
		if !agentIDPattern.MatchString(a.ID) {
			return nil, fmt.Errorf("registry: invalid agent id %q", a.ID)
		}
		if ids[a.ID] {
			return nil, fmt.Errorf("registry: duplicate agent id %q", a.ID)
		}
		ids[a.ID] = true
		if a.Kind == "" {
			a.Kind = "ai"
		}
		if a.Kind != "ai" && a.Kind != "automation" {
			return nil, fmt.Errorf("registry: agent %q has invalid kind %q", a.ID, a.Kind)
		}
		if len(a.Logins)+len(a.BranchPrefixes)+len(a.TrailerEmails) == 0 {
			return nil, fmt.Errorf("registry: agent %q has no logins, branch prefixes, or trailer emails", a.ID)
		}
		for _, l := range a.Logins {
			k := strings.ToLower(strings.TrimSpace(l))
			if k == "" {
				return nil, fmt.Errorf("registry: agent %q has an empty login", a.ID)
			}
			if owner, dup := logins[k]; dup {
				return nil, fmt.Errorf("registry: login %q is claimed by both %q and %q", l, owner, a.ID)
			}
			logins[k] = a.ID
		}
		for _, p := range a.BranchPrefixes {
			if p == "" || !strings.HasSuffix(p, "/") {
				return nil, fmt.Errorf("registry: agent %q branch prefix %q must be non-empty and end with /", a.ID, p)
			}
			if owner, dup := prefixes[p]; dup {
				return nil, fmt.Errorf("registry: branch prefix %q is claimed by both %q and %q", p, owner, a.ID)
			}
			prefixes[p] = a.ID
		}
		for _, e := range a.TrailerEmails {
			k := strings.ToLower(strings.TrimSpace(e))
			if !strings.Contains(k, "@") {
				return nil, fmt.Errorf("registry: agent %q trailer email %q is not an address", a.ID, e)
			}
			if owner, dup := emails[k]; dup {
				return nil, fmt.Errorf("registry: trailer email %q is claimed by both %q and %q", e, owner, a.ID)
			}
			emails[k] = a.ID
		}
	}
	return &r, nil
}

// Attribution says which identity a piece of work belongs to and how that was
// decided. Only login attribution is verified by GitHub; branch prefixes and
// commit trailers are self-reported by whoever pushed the branch.
type Attribution struct {
	AgentID  string `json:"agent_id"`
	Platform string `json:"platform"`
	Kind     string `json:"kind"`   // "ai", "automation", "human", or "unknown"
	Source   string `json:"source"` // "login", "branch", "trailer", "human", or "unattributed"
	Evidence string `json:"evidence"`
	Verified bool   `json:"verified"`
}

// Label is a short human-readable name for the attributed identity.
func (a Attribution) Label() string {
	if a.AgentID == "" {
		return "unattributed"
	}
	return a.AgentID
}

func (r *Registry) byLogin(login string) *Agent {
	k := strings.ToLower(login)
	for i := range r.Agents {
		for _, l := range r.Agents[i].Logins {
			if strings.ToLower(l) == k {
				return &r.Agents[i]
			}
		}
	}
	return nil
}

func (r *Registry) isHuman(login string) bool {
	for _, h := range r.Humans {
		if strings.EqualFold(h, login) {
			return true
		}
	}
	return false
}

func fromAgent(a *Agent, source, evidence string, verified bool) Attribution {
	return Attribution{AgentID: a.ID, Platform: a.Platform, Kind: a.Kind, Source: source, Evidence: evidence, Verified: verified}
}

// Attribute identifies the agent behind work authored by login on branch,
// with the given Co-Authored-By trailer emails. Precedence: a registered
// GitHub login (verified), then the longest matching branch prefix, then a
// trailer email, then a listed human, else unattributed.
func (r *Registry) Attribute(login, branch string, trailerEmails []string) Attribution {
	if a := r.byLogin(login); a != nil {
		return fromAgent(a, "login", login, true)
	}
	var best *Agent
	bestPrefix := ""
	for i := range r.Agents {
		for _, p := range r.Agents[i].BranchPrefixes {
			if strings.HasPrefix(branch, p) && len(p) > len(bestPrefix) {
				best, bestPrefix = &r.Agents[i], p
			}
		}
	}
	if best != nil {
		return fromAgent(best, "branch", bestPrefix, false)
	}
	seen := map[string]bool{}
	for _, e := range trailerEmails {
		k := strings.ToLower(strings.TrimSpace(e))
		if seen[k] {
			continue
		}
		seen[k] = true
		for i := range r.Agents {
			for _, te := range r.Agents[i].TrailerEmails {
				if strings.ToLower(te) == k {
					return fromAgent(&r.Agents[i], "trailer", te, false)
				}
			}
		}
	}
	if r.isHuman(login) {
		return Attribution{AgentID: login, Platform: "human", Kind: "human", Source: "human", Evidence: login, Verified: true}
	}
	return Attribution{Kind: "unknown", Source: "unattributed", Evidence: login}
}

// AttributeReviewer identifies a reviewer. Reviews carry no branch or trailer,
// so only the GitHub login is used.
func (r *Registry) AttributeReviewer(login string) Attribution {
	if a := r.byLogin(login); a != nil {
		return fromAgent(a, "login", login, true)
	}
	if r.isHuman(login) {
		return Attribution{AgentID: login, Platform: "human", Kind: "human", Source: "human", Evidence: login, Verified: true}
	}
	return Attribution{AgentID: "login:" + strings.ToLower(login), Platform: "unregistered", Kind: "unknown", Source: "unattributed", Evidence: login, Verified: true}
}

var trailerPattern = regexp.MustCompile(`(?im)^co-authored-by:[^<\n]*<([^>\n]+)>\s*$`)

// TrailerEmails extracts Co-Authored-By email addresses from commit messages.
func TrailerEmails(messages []string) []string {
	set := map[string]bool{}
	for _, m := range messages {
		for _, match := range trailerPattern.FindAllStringSubmatch(m, -1) {
			set[strings.ToLower(strings.TrimSpace(match[1]))] = true
		}
	}
	out := make([]string, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}
