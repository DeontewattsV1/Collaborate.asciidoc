package broker

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"collaborate/internal/render"
	"collaborate/internal/repoops"
	"collaborate/internal/store"
)

type Server struct {
	Store         *store.Store
	LeaseTTL      time.Duration
	Gates         map[string][]repoops.Gate // trusted operator-supplied configuration, never read from an agent branch
	ExportDir     string
	AdminToken    string
	eventMu       sync.Mutex
	integrationMu sync.Mutex
}
type authInfo struct {
	session, agent, digest string
	sig                    []byte
	keyVersion             int
}
type payload struct {
	ID      string            `json:"id"`
	Repo    string            `json:"repo"`
	Agent   string            `json:"agent"`
	Model   string            `json:"model"`
	Pubkey  ed25519.PublicKey `json:"pubkey"`
	Intent  string            `json:"intent"`
	Paths   []string          `json:"paths"`
	Body    string            `json:"body"`
	Commit  string            `json:"commit"`
	Branch  string            `json:"branch"`
	DiffSHA string            `json:"diff_sha"`
	Files   []string          `json:"files"`
	Trunk   string            `json:"trunk"`
	Path    string            `json:"path"`
	Content []byte            `json:"content"`
	QueueID int64             `json:"queue_id"`
	Verdict string            `json:"verdict"`
	Reason  string            `json:"reason"`
	Version int               `json:"version"`
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func validID(s string) bool { return safeID.MatchString(s) && !strings.Contains(s, "..") }
func (s *Server) adminAuthorized(r *http.Request) bool {
	if s.AdminToken == "" {
		return true
	}
	got := r.Header.Get("X-Collab-Admin-Token")
	return len(got) == len(s.AdminToken) && subtle.ConstantTimeCompare([]byte(got), []byte(s.AdminToken)) == 1
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func requestDigest(method, path, ts, nonce string, body []byte) []byte {
	h := sha256.New()
	for _, part := range [][]byte{[]byte(method), []byte{0}, []byte(path), []byte{0}, []byte(ts), []byte{0}, []byte(nonce), []byte{0}, body} {
		_, _ = h.Write(part)
	}
	return h.Sum(nil)
}
func (s *Server) authenticate(r *http.Request, body []byte) (authInfo, error) {
	agent, ts, nonce, sigText, session := r.Header.Get("X-Collab-Agent"), r.Header.Get("X-Collab-Timestamp"), r.Header.Get("X-Collab-Nonce"), r.Header.Get("X-Collab-Signature"), r.Header.Get("X-Collab-Session")
	if agent == "" || session == "" || nonce == "" {
		return authInfo{}, fmt.Errorf("missing signed-request headers")
	}
	ms, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || time.Since(time.UnixMilli(ms)) > 5*time.Minute || time.Until(time.UnixMilli(ms)) > 5*time.Minute {
		return authInfo{}, fmt.Errorf("request timestamp outside five-minute window")
	}
	sig, err := hex.DecodeString(sigText)
	if err != nil {
		return authInfo{}, fmt.Errorf("invalid signature encoding")
	}
	a, err := s.Store.Agent(session, agent)
	if err != nil || a.KeyRevoked {
		return authInfo{}, fmt.Errorf("unknown agent/session or revoked key")
	}
	d := requestDigest(r.Method, r.URL.RequestURI(), ts, nonce, body)
	if !ed25519.Verify(a.Pubkey, d, sig) {
		return authInfo{}, fmt.Errorf("signature verification failed")
	}
	if err = s.Store.UseNonce(agent, nonce, ms); err != nil {
		return authInfo{}, fmt.Errorf("replayed request nonce")
	}
	return authInfo{session: session, agent: agent, digest: hex.EncodeToString(d), sig: sig, keyVersion: a.KeyVersion}, nil
}
func (s *Server) signed(fn func(http.ResponseWriter, *http.Request, authInfo, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": "cannot read request"})
			return
		}
		a, err := s.authenticate(r, body)
		if err != nil {
			writeJSON(w, 401, map[string]string{"error": err.Error()})
			return
		}
		fn(w, r, a, body)
	}
}
func requestSessionAllowed(w http.ResponseWriter, r *http.Request, a authInfo) bool {
	requested := r.URL.Query().Get("session")
	if requested != "" && requested != a.session {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "requested session is not authorized"})
		return false
	}
	return true
}
func decode(body []byte, p *payload) error { return json.Unmarshal(body, p) }
func (s *Server) appendEvent(a authInfo, typ string, data any) (store.Event, error) {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	e, err := s.Store.AppendVersion(a.session, a.agent, typ, data, a.digest, a.sig, a.keyVersion)
	if err != nil {
		return e, err
	}
	if s.ExportDir != "" {
		if err = s.writeExport([]store.Event{e}); err != nil {
			log.Printf("collaborate: durable export append failed after event %d: %v", e.Seq, err)
		}
	}
	return e, nil
}
func (s *Server) writeExport(events []store.Event) error {
	if s.ExportDir == "" || len(events) == 0 {
		return nil
	}
	if err := os.MkdirAll(s.ExportDir, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.ExportDir, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, e := range events {
		if err = enc.Encode(e); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
func (s *Server) syncExport() error {
	if s.ExportDir == "" {
		return nil
	}
	if _, err := s.Store.Verify(); err != nil {
		return fmt.Errorf("ledger verification before export: %w", err)
	}
	all, err := s.Store.AllEvents()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(s.ExportDir, 0700); err != nil {
		return err
	}
	name := filepath.Join(s.ExportDir, "events.jsonl")
	f, err := os.Open(name)
	start := 0
	if err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
		for sc.Scan() {
			var e store.Event
			if err = json.Unmarshal(sc.Bytes(), &e); err != nil {
				f.Close()
				return fmt.Errorf("invalid backup event line %d: %w", start+1, err)
			}
			if start >= len(all) {
				f.Close()
				return fmt.Errorf("backup contains events absent from ledger")
			}
			want, _ := json.Marshal(all[start])
			got, _ := json.Marshal(e)
			if !bytes.Equal(want, got) {
				f.Close()
				return fmt.Errorf("backup diverges from ledger at event %d", start+1)
			}
			start++
		}
		if err = sc.Err(); err != nil {
			f.Close()
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return s.writeExport(all[start:])
}
func (s *Server) append(w http.ResponseWriter, a authInfo, typ string, data any) {
	e, err := s.appendEvent(a, typ, data)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"seq": e.Seq, "hash": e.Hash})
}
func (s *Server) gates(session string) []repoops.Gate { return s.Gates[session] }
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if !s.adminAuthorized(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "operator token required"})
			return
		}
		var in payload
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		id := in.ID
		if id == "" {
			writeJSON(w, 400, map[string]string{"error": "session id is required"})
			return
		}
		if !validID(id) {
			writeJSON(w, 400, map[string]string{"error": "invalid session id"})
			return
		}
		trunk := in.Trunk
		if trunk == "" {
			trunk = "collab/" + id + "/trunk"
		}
		if !repoops.ValidBranch(trunk) {
			writeJSON(w, 400, map[string]string{"error": "invalid integration branch"})
			return
		}
		if in.Repo != "" {
			if err := repoops.EnsureBranch(in.Repo, trunk); err != nil {
				writeJSON(w, 400, map[string]string{"error": "cannot initialize integration branch: " + err.Error()})
				return
			}
		}
		if err := s.Store.CreateSessionWithTrunk(id, in.Repo, trunk); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 201, map[string]string{"session": id, "trunk": trunk})
	})
	mux.HandleFunc("POST /v1/agents", func(w http.ResponseWriter, r *http.Request) {
		if !s.adminAuthorized(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "operator token required"})
			return
		}
		session := r.URL.Query().Get("session")
		var in payload
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		if !validID(in.Agent) || in.Model == "" {
			writeJSON(w, 400, map[string]string{"error": "valid agent and model are required"})
			return
		}
		if err := s.Store.Register(session, in.Agent, in.Model, in.Pubkey); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 201, map[string]string{"agent": in.Agent, "model": in.Model})
	})
	mux.HandleFunc("POST /v1/claim", s.signed(func(w http.ResponseWriter, r *http.Request, a authInfo, b []byte) {
		var in payload
		if decode(b, &in) != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		if err := s.Store.Claim(a.session, a.agent, in.Intent, in.Paths, s.LeaseTTL); err != nil {
			writeJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}
		s.append(w, a, "CLAIM", map[string]any{"intent": in.Intent, "paths": in.Paths})
	}))
	mux.HandleFunc("POST /v1/release", s.signed(func(w http.ResponseWriter, r *http.Request, a authInfo, b []byte) {
		var in payload
		if decode(b, &in) != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		if err := s.Store.Release(a.session, a.agent, in.Paths); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		s.append(w, a, "RELEASE", map[string]any{"paths": in.Paths})
	}))
	mux.HandleFunc("POST /v1/renew", s.signed(func(w http.ResponseWriter, r *http.Request, a authInfo, b []byte) {
		exp, err := s.Store.Renew(a.session, a.agent, s.LeaseTTL)
		if err != nil {
			writeJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}
		s.append(w, a, "RENEW", map[string]any{"expires_ms": exp})
	}))
	mux.HandleFunc("POST /v1/say", s.signed(func(w http.ResponseWriter, r *http.Request, a authInfo, b []byte) {
		var in payload
		if decode(b, &in) != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		s.append(w, a, "SAY", map[string]string{"body": in.Body})
	}))
	mux.HandleFunc("POST /v1/key/rotate", s.signed(func(w http.ResponseWriter, r *http.Request, a authInfo, b []byte) {
		var in payload
		if decode(b, &in) != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		v, err := s.Store.RotateKeyFrom(a.session, a.agent, a.keyVersion, in.Pubkey)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		e, err := s.appendEvent(a, "KEY_ROTATE", map[string]any{"new_version": v, "public_key_sha256": fmt.Sprintf("%x", sha256.Sum256(in.Pubkey))})
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"version": v, "seq": e.Seq, "hash": e.Hash})
	}))
	mux.HandleFunc("POST /v1/key/revoke", s.signed(func(w http.ResponseWriter, r *http.Request, a authInfo, b []byte) {
		var in payload
		if decode(b, &in) != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		if err := s.Store.RevokeKey(a.session, a.agent, in.Version); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		s.append(w, a, "KEY_REVOKE", map[string]any{"version": in.Version})
	}))
	mux.HandleFunc("POST /v1/receipt", s.signed(func(w http.ResponseWriter, r *http.Request, a authInfo, b []byte) {
		var in payload
		if decode(b, &in) != nil || in.Commit == "" {
			writeJSON(w, 400, map[string]string{"error": "commit SHA is required"})
			return
		}
		root, err := s.Store.Session(a.session)
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": "unknown session"})
			return
		}
		wt, err := s.Store.AgentWorktree(a.session, a.agent)
		if err != nil {
			writeJSON(w, 409, map[string]string{"error": "agent has no registered isolated worktree"})
			return
		}
		isHead, err := repoops.CommitIsBranchHead(root, wt.Branch, in.Commit)
		if err != nil || !isHead {
			writeJSON(w, 409, map[string]string{"error": "receipt must name the current commit at the agent's registered branch tip"})
			return
		}
		rec, err := repoops.InspectCommit(root, in.Commit)
		if err != nil {
			writeJSON(w, 422, map[string]string{"error": "server could not verify commit: " + err.Error()})
			return
		}
		for _, f := range rec.Files {
			ok, leaseErr := s.Store.LeaseAllows(a.session, a.agent, f)
			if leaseErr != nil || !ok {
				writeJSON(w, 409, map[string]string{"error": "agent has no active lease covering changed path " + f})
				return
			}
		}
		gateResults := []repoops.GateResult{}
		gateErr := error(nil)
		if len(s.gates(a.session)) > 0 {
			gateResults, gateErr = repoops.RunGates(root, rec.Commit, s.gates(a.session))
		}
		data := map[string]any{"commit": rec.Commit, "tree": rec.Tree, "diff_sha": rec.DiffSHA, "files": rec.Files, "server_verified": true, "gates": gateResults, "gate_status": "not_configured"}
		if len(s.gates(a.session)) > 0 {
			data["gate_status"] = "passed"
			if gateErr != nil {
				data["gate_status"] = "failed"
				data["gate_error"] = gateErr.Error()
			}
		}
		e, err := s.appendEvent(a, "RECEIPT", data)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		if gateErr != nil {
			writeJSON(w, 422, map[string]any{"error": gateErr.Error(), "seq": e.Seq, "hash": e.Hash, "gate_status": "failed"})
			return
		}
		writeJSON(w, 200, map[string]any{"seq": e.Seq, "hash": e.Hash, "receipt": data})
	}))
	mux.HandleFunc("POST /v1/worktree", s.signed(func(w http.ResponseWriter, r *http.Request, a authInfo, b []byte) {
		var in payload
		if decode(b, &in) != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		session, err := s.Store.SessionInfo(a.session)
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": "unknown session"})
			return
		}
		if session.Repo == "" || session.TrunkBranch == "" {
			writeJSON(w, 409, map[string]string{"error": "session has no server-side Git checkout or trunk"})
			return
		}
		branch := in.Branch
		if branch == "" {
			branch = "collab/" + a.session + "/" + a.agent
		}
		if !repoops.ValidBranch(branch) {
			writeJSON(w, 400, map[string]string{"error": "invalid branch name"})
			return
		}
		prefix := "collab/" + a.session + "/" + a.agent
		if branch != prefix && !strings.HasPrefix(branch, prefix+"-") {
			writeJSON(w, 403, map[string]string{"error": "agent branches must use the collab/<session>/<agent> namespace"})
			return
		}
		wt := filepath.Join(session.Repo, ".collab", "wt", a.agent)
		if err := repoops.CreateWorktree(session.Repo, branch, wt, session.TrunkBranch); err != nil {
			writeJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}
		if err = s.Store.SetWorktree(a.session, a.agent, wt, branch); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		e, err := s.appendEvent(a, "WORKTREE", map[string]string{"branch": branch, "path": wt})
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 201, map[string]any{"path": wt, "branch": branch, "seq": e.Seq})
	}))
	mux.HandleFunc("POST /v1/write", s.signed(func(w http.ResponseWriter, r *http.Request, a authInfo, b []byte) {
		var in payload
		if decode(b, &in) != nil || in.Path == "" {
			writeJSON(w, 400, map[string]string{"error": "path and content are required"})
			return
		}
		ok, err := s.Store.LeaseAllows(a.session, a.agent, in.Path)
		if err != nil || !ok {
			writeJSON(w, 409, map[string]string{"error": "write blocked: no active lease covers path"})
			return
		}
		wt, err := s.Store.AgentWorktree(a.session, a.agent)
		if err != nil {
			writeJSON(w, 409, map[string]string{"error": "agent has no registered worktree"})
			return
		}
		if err = writeFileUnder(wt.Path, in.Path, in.Content); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		s.append(w, a, "WRITE", map[string]any{"path": in.Path, "bytes": len(in.Content), "sha256": fmt.Sprintf("%x", sha256.Sum256(in.Content))})
	}))
	mux.HandleFunc("POST /v1/submit", s.signed(func(w http.ResponseWriter, r *http.Request, a authInfo, b []byte) {
		var in payload
		if decode(b, &in) != nil || in.Commit == "" {
			writeJSON(w, 400, map[string]string{"error": "commit SHA is required"})
			return
		}
		wt, err := s.Store.AgentWorktree(a.session, a.agent)
		if err != nil {
			writeJSON(w, 409, map[string]string{"error": "agent has no registered worktree"})
			return
		}
		if in.Branch != "" && in.Branch != wt.Branch {
			writeJSON(w, 403, map[string]string{"error": "agent may submit only its registered branch"})
			return
		}
		root, err := s.Store.Session(a.session)
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": "unknown session"})
			return
		}
		isHead, err := repoops.CommitIsBranchHead(root, wt.Branch, in.Commit)
		if err != nil || !isHead {
			writeJSON(w, 409, map[string]string{"error": "only the registered branch tip may be submitted"})
			return
		}
		ok, err := s.Store.HasReceipt(a.session, a.agent, in.Commit)
		if err != nil || !ok {
			writeJSON(w, 409, map[string]string{"error": "a server-verified receipt is required before submission"})
			return
		}
		id, err := s.Store.Enqueue(a.session, a.agent, in.Commit, wt.Branch)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		e, err := s.appendEvent(a, "SUBMIT", map[string]any{"queue_id": id, "commit": in.Commit, "branch": wt.Branch})
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 201, map[string]any{"queue_id": id, "seq": e.Seq})
	}))
	mux.HandleFunc("POST /v1/review", s.signed(func(w http.ResponseWriter, r *http.Request, a authInfo, b []byte) {
		var in payload
		if decode(b, &in) != nil || in.QueueID < 1 {
			writeJSON(w, 400, map[string]string{"error": "queue_id is required"})
			return
		}
		if err := s.Store.Review(a.session, in.QueueID, a.agent, in.Verdict, in.Reason); err != nil {
			writeJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}
		s.append(w, a, "REVIEW", map[string]any{"queue_id": in.QueueID, "verdict": in.Verdict, "reason": in.Reason})
	}))
	mux.HandleFunc("POST /v1/integrate", s.signed(func(w http.ResponseWriter, r *http.Request, a authInfo, b []byte) {
		var in payload
		if decode(b, &in) != nil || in.QueueID < 1 {
			writeJSON(w, 400, map[string]string{"error": "queue_id is required"})
			return
		}
		s.integrationMu.Lock()
		defer s.integrationMu.Unlock()
		q, err := s.Store.QueueItem(in.QueueID)
		if err != nil || q.Session != a.session {
			writeJSON(w, 404, map[string]string{"error": "queue item not found"})
			return
		}
		if q.State != "pending" {
			writeJSON(w, 409, map[string]string{"error": "queue item is not pending"})
			return
		}
		ok, err := s.Store.HasIndependentApproval(q.ID, q.Agent)
		if err != nil || !ok {
			writeJSON(w, 409, map[string]string{"error": "an independent reviewer approval is required"})
			return
		}
		gates := s.gates(a.session)
		if len(gates) == 0 {
			writeJSON(w, 409, map[string]string{"error": "no trusted gates configured; refusing integration"})
			return
		}
		root, err := s.Store.Session(a.session)
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": "unknown session"})
			return
		}
		info, err := s.Store.SessionInfo(a.session)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		merged, results, err := repoops.MergeQueueCommitWithGates(root, info.TrunkBranch, q.Commit, gates)
		if err != nil {
			if errors.Is(err, repoops.ErrBranchMoved) {
				event, eventErr := s.appendEvent(a, "MERGE_RETRY", map[string]any{"queue_id": q.ID, "error": err.Error()})
				if eventErr != nil {
					writeJSON(w, 500, map[string]string{"error": eventErr.Error()})
					return
				}
				writeJSON(w, 409, map[string]any{"error": err.Error(), "seq": event.Seq, "hash": event.Hash, "queue_state": "pending"})
				return
			}
			if stateErr := s.Store.SetQueueState(q.ID, "blocked", err.Error()); stateErr != nil {
				writeJSON(w, 500, map[string]string{"error": stateErr.Error()})
				return
			}
			typ := "MERGE_FAIL"
			if errors.Is(err, repoops.ErrGateFailed) {
				typ = "GATE_FAIL"
			}
			event, eventErr := s.appendEvent(a, typ, map[string]any{"queue_id": q.ID, "results": results, "error": err.Error()})
			if eventErr != nil {
				writeJSON(w, 500, map[string]string{"error": eventErr.Error()})
				return
			}
			status := http.StatusConflict
			if errors.Is(err, repoops.ErrGateFailed) {
				status = http.StatusUnprocessableEntity
			}
			writeJSON(w, status, map[string]any{"error": err.Error(), "seq": event.Seq, "hash": event.Hash, "gate_status": map[bool]string{true: "failed", false: "not_applicable"}[errors.Is(err, repoops.ErrGateFailed)]})
			return
		}
		event, err := s.appendEvent(a, "MERGE", map[string]any{"queue_id": q.ID, "commit": q.Commit, "trunk": info.TrunkBranch, "merge_head": merged, "gates": results})
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		if err = s.Store.SetQueueState(q.ID, "merged", ""); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"seq": event.Seq, "hash": event.Hash, "merge_head": merged})
	}))
	mux.HandleFunc("GET /v1/leases", s.signed(func(w http.ResponseWriter, r *http.Request, a authInfo, _ []byte) {
		if !requestSessionAllowed(w, r, a) {
			return
		}
		ls, err := s.Store.Leases(a.session)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"leases": ls})
	}))
	mux.HandleFunc("GET /v1/events", s.signed(func(w http.ResponseWriter, r *http.Request, a authInfo, _ []byte) {
		if !requestSessionAllowed(w, r, a) {
			return
		}
		ev, err := s.Store.Events(a.session)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, ev)
	}))
	mux.HandleFunc("GET /v1/queue", s.signed(func(w http.ResponseWriter, r *http.Request, a authInfo, _ []byte) {
		if !requestSessionAllowed(w, r, a) {
			return
		}
		q, err := s.Store.Queue(a.session)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"queue": q})
	}))
	mux.HandleFunc("GET /v1/verify", s.signed(func(w http.ResponseWriter, r *http.Request, a authInfo, _ []byte) {
		n, err := s.Store.Verify()
		if err != nil {
			writeJSON(w, 409, map[string]any{"ok": false, "checked": n, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "checked": n})
	}))
	mux.HandleFunc("GET /v1/doc", s.signed(func(w http.ResponseWriter, r *http.Request, a authInfo, _ []byte) {
		if !requestSessionAllowed(w, r, a) {
			return
		}
		ev, err := s.Store.Events(a.session)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "text/asciidoc; charset=utf-8")
		_, _ = w.Write(render.AsciiDoc(a.session, ev))
	}))
	mux.HandleFunc("GET /v1/export", func(w http.ResponseWriter, r *http.Request) {
		if !s.adminAuthorized(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "operator token required"})
			return
		}
		if _, verifyErr := s.Store.Verify(); verifyErr != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "ledger verification failed: " + verifyErr.Error()})
			return
		}
		session := r.URL.Query().Get("session")
		var ev []store.Event
		var err error
		if session == "" {
			ev, err = s.Store.AllEvents()
		} else {
			ev, err = s.Store.Events(session)
		}
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, ev)
	})
	return mux
}
func (s *Server) Listen(addr string) error { return s.ListenTLS(addr, "", "", "") }
func (s *Server) ListenTLS(addr, certFile, keyFile, clientCAFile string) error {
	if err := s.syncExport(); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	loopback := ip != nil && ip.IsLoopback()
	if !loopback && (certFile == "" || keyFile == "" || clientCAFile == "" || s.AdminToken == "") {
		return fmt.Errorf("non-loopback bind requires server TLS, mandatory mTLS, and an operator token")
	}
	if (certFile == "") != (keyFile == "") {
		return fmt.Errorf("TLS certificate and key must be supplied together")
	}
	httpServer := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second}
	if certFile == "" {
		return httpServer.ListenAndServe()
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return err
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	if clientCAFile != "" {
		pem, err := os.ReadFile(clientCAFile)
		if err != nil {
			return err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return fmt.Errorf("client CA file contains no certificates")
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	httpServer.TLSConfig = cfg
	return httpServer.ListenAndServeTLS("", "")
}
