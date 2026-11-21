// Package agentregistry validates repository-controlled agent identities and
// provides fail-closed runtime authorization for MCP and provider-adapter tools.
package agentregistry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

const MaxRegistryBytes = 1 << 20

type Registry struct {
	Schema        string  `json:"$schema"`
	SchemaVersion int     `json:"schema_version"`
	Agents        []Agent `json:"agents"`
}

type Agent struct {
	ID               string           `json:"agent_id"`
	DisplayName      string           `json:"display_name"`
	Status           string           `json:"status"`
	GitHubIdentities []GitHubIdentity `json:"github_identities,omitempty"`
	BranchPrefixes   []string         `json:"branch_prefixes,omitempty"`
	AllowedTools     []string         `json:"allowed_tools"`
}

type GitHubIdentity struct {
	AccountType  string `json:"account_type"`
	Login        string `json:"login"`
	GitHubUserID int64  `json:"github_user_id"`
}

type PullRequestIdentity struct {
	AccountType  string
	Login        string
	GitHubUserID int64
}

var (
	agentIDPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	userLoginPattern   = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
	botLoginPattern    = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?\[bot\]$`)
	mcpToolPattern     = regexp.MustCompile(`^mcp:[a-z][a-z0-9_-]*$`)
	adapterToolPattern = regexp.MustCompile(`^adapter:[A-Za-z_][A-Za-z0-9_.-]*$`)
)

// Load reads and strictly validates a JSON registry. Unknown keys and trailing
// JSON values are rejected so policy typos cannot silently broaden access.
func Load(path string) (*Registry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open agent registry: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxRegistryBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read agent registry: %w", err)
	}
	if len(data) > MaxRegistryBytes {
		return nil, errors.New("agent registry exceeds 1 MiB")
	}
	return Parse(data)
}

// Parse strictly decodes and validates registry data.
func Parse(data []byte) (*Registry, error) {
	if err := validateRequiredJSONFields(data); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var r Registry
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("decode agent registry: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("agent registry contains multiple JSON values")
		}
		return nil, fmt.Errorf("decode trailing agent registry data: %w", err)
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return &r, nil
}

func validateRequiredJSONFields(data []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("decode agent registry object: %w", err)
	}
	if root == nil {
		return errors.New("agent registry must be a JSON object")
	}
	for _, key := range []string{"$schema", "schema_version", "agents"} {
		if _, ok := root[key]; !ok {
			return fmt.Errorf("agent registry field %q is required", key)
		}
	}
	var agents []json.RawMessage
	if err := json.Unmarshal(root["agents"], &agents); err != nil || agents == nil {
		return errors.New("agent registry field \"agents\" must be an array")
	}
	for i, raw := range agents {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
			return fmt.Errorf("agents[%d] must be an object", i)
		}
		for _, key := range []string{"agent_id", "display_name", "status", "allowed_tools"} {
			if _, ok := fields[key]; !ok {
				return fmt.Errorf("agents[%d].%s is required", i, key)
			}
		}
		var status string
		if err := json.Unmarshal(fields["status"], &status); err != nil {
			return fmt.Errorf("agents[%d].status must be a string", i)
		}
		if status == "active" || status == "suspended" {
			if _, ok := fields["github_identities"]; !ok {
				return fmt.Errorf("agents[%d].github_identities is required for %s agents", i, status)
			}
		}
		if status == "unconfigured" {
			if _, ok := fields["github_identities"]; ok {
				return fmt.Errorf("agents[%d] unconfigured entries must omit github_identities", i)
			}
		}
		for _, key := range []string{"allowed_tools", "branch_prefixes", "github_identities"} {
			rawList, ok := fields[key]
			if !ok {
				continue
			}
			var values []json.RawMessage
			if err := json.Unmarshal(rawList, &values); err != nil || values == nil {
				return fmt.Errorf("agents[%d].%s must be an array", i, key)
			}
			if key != "allowed_tools" && len(values) == 0 {
				return fmt.Errorf("agents[%d].%s must not be empty when present", i, key)
			}
			if key == "github_identities" {
				for j, identityRaw := range values {
					var identity map[string]json.RawMessage
					if err := json.Unmarshal(identityRaw, &identity); err != nil || identity == nil {
						return fmt.Errorf("agents[%d].github_identities[%d] must be an object", i, j)
					}
					for _, identityKey := range []string{"account_type", "login", "github_user_id"} {
						if _, ok := identity[identityKey]; !ok {
							return fmt.Errorf("agents[%d].github_identities[%d].%s is required", i, j, identityKey)
						}
					}
				}
			}
		}
	}
	return nil
}

// Validate applies individual and cross-entry invariants shared by CI and runtime.
func (r *Registry) Validate() error {
	if r == nil {
		return errors.New("agent registry is required")
	}
	if r.Schema != "./agent-registry.schema.json" {
		return errors.New("$schema must be ./agent-registry.schema.json")
	}
	if r.SchemaVersion != 1 {
		return fmt.Errorf("unsupported agent registry schema_version %d", r.SchemaVersion)
	}
	ids := map[string]bool{}
	type prefixOwner struct{ prefix, agent string }
	var prefixes []prefixOwner
	for i := range r.Agents {
		a := &r.Agents[i]
		where := fmt.Sprintf("agents[%d]", i)
		if !agentIDPattern.MatchString(a.ID) {
			return fmt.Errorf("%s.agent_id is invalid", where)
		}
		if ids[a.ID] {
			return fmt.Errorf("duplicate agent_id %q", a.ID)
		}
		ids[a.ID] = true
		if strings.TrimSpace(a.DisplayName) == "" || len(a.DisplayName) > 100 {
			return fmt.Errorf("%s.display_name must be non-empty and at most 100 characters", where)
		}
		switch a.Status {
		case "active", "suspended":
			if len(a.GitHubIdentities) == 0 {
				return fmt.Errorf("%s.github_identities must be non-empty for %s agents", where, a.Status)
			}
		case "unconfigured":
			if len(a.GitHubIdentities) != 0 {
				return fmt.Errorf("%s unconfigured entry must not contain GitHub identities", where)
			}
		default:
			return fmt.Errorf("%s.status must be active, suspended, or unconfigured", where)
		}
		seenIdentity := map[string]bool{}
		for j, identity := range a.GitHubIdentities {
			if identity.AccountType != "user" && identity.AccountType != "bot" {
				return fmt.Errorf("%s.github_identities[%d].account_type must be user or bot", where, j)
			}
			pattern := userLoginPattern
			if identity.AccountType == "bot" {
				pattern = botLoginPattern
			}
			if !pattern.MatchString(identity.Login) || identity.GitHubUserID <= 0 {
				return fmt.Errorf("%s.github_identities[%d] has an invalid login or GitHub user ID", where, j)
			}
			key := fmt.Sprintf("%s:%d", identity.AccountType, identity.GitHubUserID)
			if seenIdentity[key] {
				return fmt.Errorf("%s has duplicate GitHub identity %s", where, key)
			}
			seenIdentity[key] = true
		}
		seenPrefix := map[string]bool{}
		for j, prefix := range a.BranchPrefixes {
			if !validBranchPrefix(prefix) {
				return fmt.Errorf("%s.branch_prefixes[%d] is not a valid branch prefix", where, j)
			}
			if seenPrefix[prefix] {
				return fmt.Errorf("%s has duplicate branch prefix %q", where, prefix)
			}
			seenPrefix[prefix] = true
			prefixes = append(prefixes, prefixOwner{prefix: prefix, agent: a.ID})
		}
		seenTool := map[string]bool{}
		for j, tool := range a.AllowedTools {
			if len(tool) > 128 || (!mcpToolPattern.MatchString(tool) && !adapterToolPattern.MatchString(tool)) {
				return fmt.Errorf("%s.allowed_tools[%d] is invalid: %q", where, j, tool)
			}
			if seenTool[tool] {
				return fmt.Errorf("%s has duplicate tool grant %q", where, tool)
			}
			seenTool[tool] = true
		}
	}
	sort.Slice(prefixes, func(i, j int) bool { return prefixes[i].prefix < prefixes[j].prefix })
	for i := 0; i < len(prefixes); i++ {
		for j := i + 1; j < len(prefixes); j++ {
			if strings.HasPrefix(prefixes[j].prefix, prefixes[i].prefix) {
				return fmt.Errorf("overlapping branch prefixes %q (%s) and %q (%s)", prefixes[i].prefix, prefixes[i].agent, prefixes[j].prefix, prefixes[j].agent)
			}
		}
	}
	return nil
}

func validBranchPrefix(prefix string) bool {
	if len(prefix) < 2 || len(prefix) > 255 || !strings.HasSuffix(prefix, "/") || strings.Contains(prefix, "//") || strings.Contains(prefix, "..") || strings.Contains(prefix, "@{") || strings.ContainsAny(prefix, " ~^:?*[\\") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimSuffix(prefix, "/"), "/") {
		if segment == "" || segment == "." || strings.HasPrefix(segment, ".") || strings.HasSuffix(segment, ".lock") {
			return false
		}
		for _, r := range segment {
			if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("_-. ", r)) || r == ' ' {
				return false
			}
		}
	}
	return true
}

// Agent finds an entry by its stable internal ID.
func (r *Registry) Agent(id string) (Agent, bool) {
	if r == nil {
		return Agent{}, false
	}
	for _, a := range r.Agents {
		if a.ID == id {
			return a, true
		}
	}
	return Agent{}, false
}

// Authorize permits only exact tool grants on an active registry entry.
func (r *Registry) Authorize(agentID, tool string) error {
	a, ok := r.Agent(agentID)
	if !ok {
		return fmt.Errorf("agent %q is not registered", agentID)
	}
	if a.Status != "active" {
		return fmt.Errorf("agent %q is %s; runtime tools are denied", agentID, a.Status)
	}
	for _, grant := range a.AllowedTools {
		if grant == tool {
			return nil
		}
	}
	return fmt.Errorf("agent %q is not authorized for tool %q", agentID, tool)
}

// ResolvePullRequestAgent resolves a PR author plus branch name to one active
// agent. A branch prefix cannot authenticate an unknown author.
func (r *Registry) ResolvePullRequestAgent(author PullRequestIdentity, branch string) (Agent, error) {
	if r == nil {
		return Agent{}, errors.New("agent registry is required")
	}
	if author.GitHubUserID <= 0 || (author.AccountType != "user" && author.AccountType != "bot") {
		return Agent{}, errors.New("pull request author identity is invalid")
	}
	active := map[string]Agent{}
	for _, a := range r.Agents {
		if a.Status != "active" {
			continue
		}
		for _, id := range a.GitHubIdentities {
			if id.AccountType == author.AccountType && id.GitHubUserID == author.GitHubUserID {
				active[a.ID] = a
			}
		}
	}
	if len(active) == 0 {
		return Agent{}, fmt.Errorf("GitHub author %q (ID %d) is not an active registered agent", author.Login, author.GitHubUserID)
	}
	var matchedPrefixAgent string
	for _, a := range r.Agents {
		for _, prefix := range a.BranchPrefixes {
			if strings.HasPrefix(branch, prefix) {
				if a.Status != "active" {
					return Agent{}, fmt.Errorf("branch prefix %q belongs to %s agent %q", prefix, a.Status, a.ID)
				}
				matchedPrefixAgent = a.ID
			}
		}
	}
	if matchedPrefixAgent != "" {
		if _, ok := active[matchedPrefixAgent]; !ok {
			return Agent{}, fmt.Errorf("branch prefix identifies agent %q but the PR author identity does not", matchedPrefixAgent)
		}
		return active[matchedPrefixAgent], nil
	}
	if len(active) != 1 {
		return Agent{}, fmt.Errorf("GitHub author identity is shared by %d active agents; a registered branch prefix is required", len(active))
	}
	for _, a := range active {
		return a, nil
	}
	return Agent{}, errors.New("unable to resolve pull request agent")
}

// PathOverlap reports exact and ancestor/descendant conflicts. Inputs are
// repository-relative slash-separated paths, as returned by the GitHub API.
func PathOverlap(a, b string) bool {
	a = strings.TrimPrefix(path.Clean("/"+a), "/")
	b = strings.TrimPrefix(path.Clean("/"+b), "/")
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

type FileConflict struct {
	Path string
	PR   int
	URL  string
}

// FindFileConflicts returns each distinct current/other-PR overlapping path.
func FindFileConflicts(current []string, otherPRFiles map[int][]string, urls map[int]string) []FileConflict {
	var conflicts []FileConflict
	seen := map[string]bool{}
	for number, paths := range otherPRFiles {
		for _, currentPath := range current {
			for _, otherPath := range paths {
				if !PathOverlap(currentPath, otherPath) {
					continue
				}
				key := fmt.Sprintf("%d:%s:%s", number, currentPath, otherPath)
				if seen[key] {
					continue
				}
				seen[key] = true
				conflicts = append(conflicts, FileConflict{Path: currentPath, PR: number, URL: urls[number]})
			}
		}
	}
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].PR == conflicts[j].PR {
			return conflicts[i].Path < conflicts[j].Path
		}
		return conflicts[i].PR < conflicts[j].PR
	})
	return conflicts
}
