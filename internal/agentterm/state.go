package agentterm

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"collaborate/internal/agentregistry"
)

// Session is one clock-in of one agent instance.
type Session struct {
	ID          string `json:"id"`
	Agent       string `json:"agent"`
	Instance    string `json:"instance"`
	Branch      string `json:"branch"`
	GitHubLogin string `json:"github_login"`
	GitHubID    int64  `json:"github_id"`
	PubKey      string `json:"pubkey"`
	ClockedIn   string `json:"clocked_in"`
	LastSeen    string `json:"last_seen"`
	Open        bool   `json:"open"`
	EndReason   string `json:"end_reason,omitempty"`
	nonce       int64
	lastSeen    time.Time
}

// Claim is an exclusive lease on a repository path, held by an agent
// instance ("owner"). It survives a rejoin of the same instance and is
// released on clock-out or expiry.
type Claim struct {
	Path     string `json:"path"`
	Agent    string `json:"agent"`
	Instance string `json:"instance"`
	Seq      int64  `json:"seq"`
}

// Finding is a review finding pinned to an exact commit.
type Finding struct {
	Seq      int64  `json:"seq"`
	By       string `json:"by"`
	PR       int    `json:"pr"`
	Commit   string `json:"commit"`
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	Severity string `json:"severity"`
	Text     string `json:"text"`
	Status   string `json:"status"`
	StatusBy string `json:"status_by,omitempty"`
	FixedIn  string `json:"fixed_in,omitempty"`
}

// State is the deterministic result of replaying the log. Every entry goes
// through Apply, both when it is first written and when the log is reloaded,
// so a log that violates a rule cannot be loaded.
type State struct {
	Sessions map[string]*Session `json:"sessions"`
	Claims   map[string]Claim    `json:"claims"`
	Findings map[int64]*Finding  `json:"findings"`
	Execs    map[int64]string    `json:"-"` // exec_request seq -> session, while pending
	Head     string              `json:"head"`
	Seq      int64               `json:"seq"`
	TTL      time.Duration       `json:"-"`
	prefixes map[string][]string
	pubkeys  map[string]bool
	policy   ExecPolicy
}

// NewState creates an empty state bound to the registry's branch prefixes.
func NewState(reg *agentregistry.Registry, ttl time.Duration, policy ExecPolicy) *State {
	s := &State{
		Sessions: map[string]*Session{}, Claims: map[string]Claim{}, Findings: map[int64]*Finding{},
		Execs: map[int64]string{}, Seq: -1, TTL: ttl, prefixes: map[string][]string{},
		pubkeys: map[string]bool{}, policy: policy,
	}
	if reg != nil {
		for _, a := range reg.Agents {
			s.prefixes[a.ID] = append([]string(nil), a.BranchPrefixes...)
		}
	}
	return s
}

var (
	shaPattern      = regexp.MustCompile(`^[0-9a-f]{40}$`)
	instancePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	severities      = map[string]bool{"blocker": true, "major": true, "minor": true, "nit": true}
)

const (
	maxText   = 4000
	maxPaths  = 50
	maxOutput = 64 << 10
)

func owner(agent, instance string) string { return agent + "/" + instance }

// Apply validates e against the current state and, only if valid, applies it.
func (s *State) Apply(e Entry) error {
	if e.Seq != s.Seq+1 {
		return fmt.Errorf("sequence %d does not follow %d", e.Seq, s.Seq)
	}
	t, err := time.Parse(time.RFC3339Nano, e.Time)
	if err != nil {
		return errors.New("bad entry time")
	}
	if err := s.apply(e, t); err != nil {
		return err
	}
	s.Seq = e.Seq
	s.Head = e.Hash
	return nil
}

func (s *State) apply(e Entry, t time.Time) error {
	switch e.Kind {
	case KindGenesis:
		if e.Seq != 0 {
			return errors.New("genesis must be the first entry")
		}
		return nil
	case KindClockIn:
		return s.clockIn(e, t)
	case KindExpire:
		var b struct {
			Session string `json:"session"`
		}
		if err := json.Unmarshal([]byte(e.Body), &b); err != nil {
			return err
		}
		sess := s.Sessions[b.Session]
		if sess == nil || !sess.Open {
			return errors.New("expire: session is not open")
		}
		if !t.After(sess.lastSeen.Add(s.TTL)) {
			return errors.New("expire: session is still within its TTL")
		}
		s.end(sess, "expired")
		return nil
	case KindExecResult:
		var b ExecResult
		if err := json.Unmarshal([]byte(e.Body), &b); err != nil {
			return err
		}
		if s.Execs[b.Request] != e.Session {
			return errors.New("exec_result does not answer a pending exec_request of this session")
		}
		delete(s.Execs, b.Request)
		return nil
	case KindChat:
		return validateChat(e.Body)
	}
	if !agentKinds[e.Kind] {
		return fmt.Errorf("unknown kind %q", e.Kind)
	}
	sess := s.Sessions[e.Session]
	if sess == nil || !sess.Open {
		return errors.New("session is not clocked in")
	}
	if sess.Agent != e.Agent {
		return errors.New("entry agent does not match the session's agent")
	}
	if t.After(sess.lastSeen.Add(s.TTL)) {
		return errors.New("session expired; clock in again")
	}
	if e.Nonce <= sess.nonce {
		return errors.New("nonce must increase (replayed or reordered request)")
	}
	if err := s.agentEntry(sess, e); err != nil {
		return err
	}
	sess.nonce = e.Nonce
	sess.lastSeen = t
	sess.LastSeen = e.Time
	return nil
}

func (s *State) clockIn(e Entry, t time.Time) error {
	var b ClockInBody
	if err := json.Unmarshal([]byte(e.Body), &b); err != nil {
		return err
	}
	if !instancePattern.MatchString(b.Instance) {
		return errors.New("instance must be 1-64 characters of letters, digits, '.', '_' or '-'")
	}
	if s.Sessions[e.Session] != nil {
		return errors.New("session id reused")
	}
	if s.pubkeys[b.PubKey] {
		return errors.New("session key reused; generate a fresh key for every clock-in")
	}
	if !s.branchAllowed(e.Agent, b.Branch) {
		return fmt.Errorf("branch %q is not under a registered prefix of agent %q", b.Branch, e.Agent)
	}
	// Rejoin: the same agent instance clocking in again supersedes its old
	// session. Its claims are kept, because they belong to the instance.
	for _, old := range s.Sessions {
		if old.Open && old.Agent == e.Agent && old.Instance == b.Instance {
			old.Open = false
			old.EndReason = "superseded by rejoin " + e.Session
		}
	}
	s.pubkeys[b.PubKey] = true
	s.Sessions[e.Session] = &Session{
		ID: e.Session, Agent: e.Agent, Instance: b.Instance, Branch: b.Branch,
		GitHubLogin: b.GitHubLogin, GitHubID: b.GitHubID, PubKey: b.PubKey,
		ClockedIn: e.Time, LastSeen: e.Time, Open: true, nonce: e.Nonce, lastSeen: t,
	}
	return nil
}

func (s *State) end(sess *Session, reason string) {
	sess.Open = false
	sess.EndReason = reason
	// Release claims only if no other open session of the same instance holds them.
	for _, other := range s.Sessions {
		if other.Open && other.Agent == sess.Agent && other.Instance == sess.Instance {
			return
		}
	}
	for p, c := range s.Claims {
		if c.Agent == sess.Agent && c.Instance == sess.Instance {
			delete(s.Claims, p)
		}
	}
	for req, id := range s.Execs {
		if id == sess.ID {
			delete(s.Execs, req)
		}
	}
}

func (s *State) branchAllowed(agent, branch string) bool {
	if branch == "" || strings.ContainsAny(branch, " ~^:?*[\\") || strings.Contains(branch, "..") {
		return false
	}
	for _, p := range s.prefixes[agent] {
		if strings.HasPrefix(branch, p) && len(branch) > len(p) {
			return true
		}
	}
	return false
}

func (s *State) agentEntry(sess *Session, e Entry) error {
	switch e.Kind {
	case KindHeartbeat:
		return nil
	case KindClockOut:
		s.end(sess, "clocked out")
		return nil
	case KindClaim, KindRelease:
		var b struct {
			Paths []string `json:"paths"`
		}
		if err := strictJSON(e.Body, &b); err != nil {
			return err
		}
		paths, err := cleanPaths(b.Paths)
		if err != nil {
			return err
		}
		if e.Kind == KindRelease {
			for _, p := range paths {
				c, ok := s.Claims[p]
				if !ok || c.Agent != sess.Agent || c.Instance != sess.Instance {
					return fmt.Errorf("release: %q is not claimed by this instance", p)
				}
			}
			for _, p := range paths {
				delete(s.Claims, p)
			}
			return nil
		}
		me := owner(sess.Agent, sess.Instance)
		for _, p := range paths {
			for _, c := range s.Claims {
				if owner(c.Agent, c.Instance) != me && agentregistry.PathOverlap(p, c.Path) {
					return fmt.Errorf("claim: %q overlaps %q held by %s (entry %d); coordinate a handoff instead of duplicating work", p, c.Path, owner(c.Agent, c.Instance), c.Seq)
				}
			}
		}
		for _, p := range paths {
			if _, held := s.Claims[p]; !held {
				s.Claims[p] = Claim{Path: p, Agent: sess.Agent, Instance: sess.Instance, Seq: e.Seq}
			}
		}
		return nil
	case KindExecRequest:
		var b ExecRequest
		if err := strictJSON(e.Body, &b); err != nil {
			return err
		}
		if err := s.policy.Check(b.Argv); err != nil {
			return err
		}
		s.Execs[e.Seq] = sess.ID
		return nil
	case KindRecord:
		var b struct {
			Argv   []string `json:"argv"`
			Exit   int      `json:"exit"`
			Output string   `json:"output"`
			Where  string   `json:"where"`
		}
		if err := strictJSON(e.Body, &b); err != nil {
			return err
		}
		if len(b.Argv) == 0 || len(b.Output) > maxOutput || len(b.Where) > 200 {
			return errors.New("record: argv is required and output is limited to 64 KiB")
		}
		return nil
	case KindCommit:
		var b struct {
			SHA    string   `json:"sha"`
			Branch string   `json:"branch"`
			PR     int      `json:"pr"`
			Paths  []string `json:"paths"`
		}
		if err := strictJSON(e.Body, &b); err != nil {
			return err
		}
		if !shaPattern.MatchString(b.SHA) {
			return errors.New("commit: sha must be a full 40-character lowercase hex SHA")
		}
		if !s.branchAllowed(sess.Agent, b.Branch) {
			return fmt.Errorf("commit: branch %q is not under agent %q's registered prefix; the terminal never records work on main or another agent's branch", b.Branch, sess.Agent)
		}
		if b.PR < 0 {
			return errors.New("commit: pr must be positive")
		}
		paths, err := cleanPaths(b.Paths)
		if err != nil && len(b.Paths) > 0 {
			return err
		}
		for _, p := range paths {
			if !s.covered(sess, p) {
				return fmt.Errorf("commit: %q is not covered by a claim of this instance; claim it first", p)
			}
		}
		return nil
	case KindFinding:
		var b struct {
			PR       int    `json:"pr"`
			Commit   string `json:"commit"`
			Path     string `json:"path"`
			Line     int    `json:"line"`
			Severity string `json:"severity"`
			Text     string `json:"text"`
		}
		if err := strictJSON(e.Body, &b); err != nil {
			return err
		}
		if b.PR <= 0 || !shaPattern.MatchString(b.Commit) {
			return errors.New("finding: pr and the exact 40-character commit SHA reviewed are required")
		}
		if !severities[b.Severity] {
			return errors.New("finding: severity must be blocker, major, minor or nit")
		}
		if b.Text == "" || len(b.Text) > maxText || b.Line < 0 {
			return errors.New("finding: text is required (max 4000 bytes)")
		}
		if b.Path != "" {
			if _, err := cleanPaths([]string{b.Path}); err != nil {
				return err
			}
		}
		s.Findings[e.Seq] = &Finding{Seq: e.Seq, By: sess.Agent, PR: b.PR, Commit: b.Commit, Path: b.Path,
			Line: b.Line, Severity: b.Severity, Text: b.Text, Status: "open"}
		return nil
	case KindFindingStatus:
		var b struct {
			Finding int64  `json:"finding"`
			Status  string `json:"status"`
			Commit  string `json:"commit"`
		}
		if err := strictJSON(e.Body, &b); err != nil {
			return err
		}
		f := s.Findings[b.Finding]
		if f == nil {
			return errors.New("finding_status: no such finding")
		}
		switch b.Status {
		case "addressed", "disputed":
			if sess.Agent == f.By {
				return errors.New("finding_status: the reviewer cannot mark their own finding addressed or disputed")
			}
			if b.Status == "addressed" && !shaPattern.MatchString(b.Commit) {
				return errors.New("finding_status: addressed requires the 40-character SHA of the fixing commit")
			}
			f.FixedIn = b.Commit
		case "accepted", "withdrawn":
			// Only the reviewer closes a finding, so an author cannot clear
			// review findings against their own work.
			if sess.Agent != f.By {
				return fmt.Errorf("finding_status: only the reviewer (%s) can %s this finding", f.By, b.Status)
			}
			if b.Status == "accepted" && f.Status != "addressed" {
				return errors.New("finding_status: a finding must be addressed before it is accepted")
			}
		default:
			return errors.New("finding_status: status must be addressed, disputed, accepted or withdrawn")
		}
		f.Status = b.Status
		f.StatusBy = sess.Agent
		return nil
	case KindNote:
		var b struct {
			Text string `json:"text"`
		}
		if err := strictJSON(e.Body, &b); err != nil {
			return err
		}
		if b.Text == "" || len(b.Text) > maxText {
			return errors.New("note: text is required (max 4000 bytes)")
		}
		return nil
	}
	return fmt.Errorf("unhandled kind %q", e.Kind)
}

func (s *State) covered(sess *Session, p string) bool {
	for _, c := range s.Claims {
		if c.Agent == sess.Agent && c.Instance == sess.Instance && (c.Path == p || strings.HasPrefix(p, c.Path+"/")) {
			return true
		}
	}
	return false
}

var chatIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)

func validateChat(body string) error {
	var b ChatBody
	if err := strictJSON(body, &b); err != nil {
		return err
	}
	if !chatIDPattern.MatchString(b.Thread) || !chatIDPattern.MatchString(b.ID) {
		return errors.New("chat: thread and id are required")
	}
	switch b.Kind {
	case "thread":
		if b.Title == "" || len(b.Title) > 80 || len(b.Members) == 0 || len(b.Members) > maxPaths {
			return errors.New("chat: thread needs a title and 1-50 members")
		}
		for _, m := range b.Members {
			if !chatIDPattern.MatchString(m) {
				return errors.New("chat: bad member id")
			}
		}
	case "message", "system":
		if b.From == "" || !chatIDPattern.MatchString(b.From) {
			return errors.New("chat: from is required")
		}
		if b.Text == "" || len(b.Text) > maxText {
			return errors.New("chat: text is required (max 4000 bytes)")
		}
		if len(b.Mentions) > 8 {
			return errors.New("chat: too many mentions")
		}
		for _, m := range b.Mentions {
			if !chatIDPattern.MatchString(m) {
				return errors.New("chat: bad mention")
			}
		}
		if b.Tokens < 0 {
			return errors.New("chat: negative token count")
		}
	default:
		return errors.New("chat: kind must be thread, message, or system")
	}
	return nil
}

func strictJSON(body string, v any) error {
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("body: %w", err)
	}
	return nil
}

func cleanPaths(in []string) ([]string, error) {
	if len(in) == 0 || len(in) > maxPaths {
		return nil, fmt.Errorf("between 1 and %d paths are required", maxPaths)
	}
	out := make([]string, 0, len(in))
	for _, p := range in {
		c := path.Clean(p)
		if p == "" || c != p || path.IsAbs(c) || c == "." || c == ".." || strings.HasPrefix(c, "../") || strings.ContainsAny(c, "\\\x00") {
			return nil, fmt.Errorf("path %q must be a clean repository-relative path", p)
		}
		out = append(out, c)
	}
	sort.Strings(out)
	return out, nil
}

// Snapshot is the read-only view served to the browser and CLI.
type Snapshot struct {
	Head     string     `json:"head"`
	Seq      int64      `json:"seq"`
	Sessions []Session  `json:"sessions"`
	Claims   []Claim    `json:"claims"`
	Findings []Finding  `json:"findings"`
	Now      string     `json:"now"`
	TTL      string     `json:"ttl"`
	Exec     ExecPolicy `json:"exec"`
}

// Snapshot returns a sorted copy of the state.
func (s *State) Snapshot(now time.Time) Snapshot {
	snap := Snapshot{Head: s.Head, Seq: s.Seq, Now: now.UTC().Format(time.RFC3339Nano), TTL: s.TTL.String(), Exec: s.policy}
	for _, sess := range s.Sessions {
		snap.Sessions = append(snap.Sessions, *sess)
	}
	sort.Slice(snap.Sessions, func(i, j int) bool { return snap.Sessions[i].ClockedIn > snap.Sessions[j].ClockedIn })
	for _, c := range s.Claims {
		snap.Claims = append(snap.Claims, c)
	}
	sort.Slice(snap.Claims, func(i, j int) bool { return snap.Claims[i].Path < snap.Claims[j].Path })
	for _, f := range s.Findings {
		snap.Findings = append(snap.Findings, *f)
	}
	sort.Slice(snap.Findings, func(i, j int) bool { return snap.Findings[i].Seq < snap.Findings[j].Seq })
	return snap
}

// Expired lists open sessions whose TTL has lapsed at now.
func (s *State) Expired(now time.Time) []string {
	var ids []string
	for id, sess := range s.Sessions {
		if sess.Open && now.After(sess.lastSeen.Add(s.TTL)) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
