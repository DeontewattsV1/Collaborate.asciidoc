package store

import (
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrLeaseHeld    = errors.New("path is leased by another agent")
	ErrNotOwner     = errors.New("agent does not own an active lease")
	ErrRevoked      = errors.New("agent key is revoked")
	ErrStaleKey     = errors.New("request used a stale key version")
	ErrQueueSession = errors.New("queue item does not belong to reviewer session")
)

type Event struct {
	Seq           int64           `json:"seq"`
	Session       string          `json:"session"`
	Agent         string          `json:"agent"`
	Type          string          `json:"type"`
	Payload       json.RawMessage `json:"payload"`
	TS            int64           `json:"ts"`
	PrevHash      string          `json:"prev_hash"`
	RequestDigest string          `json:"request_digest"`
	Signature     []byte          `json:"signature"`
	KeyVersion    int             `json:"key_version"`
	Hash          string          `json:"hash"`
}

type Agent struct {
	ID         string
	Session    string
	Model      string
	Pubkey     ed25519.PublicKey
	KeyVersion int
	KeyRevoked bool
}

type SessionInfo struct{ ID, Repo, TrunkBranch string }
type QueueItem struct {
	ID        int64  `json:"id"`
	Session   string `json:"session"`
	Agent     string `json:"agent"`
	Commit    string `json:"commit"`
	Branch    string `json:"branch"`
	State     string `json:"state"`
	Reason    string `json:"reason"`
	CreatedMS int64  `json:"created_ms"`
}
type Worktree struct {
	Session string
	Agent   string
	Path    string
	Branch  string
}
type Store struct{ db *sql.DB }

func Open(filename string) (*Store, error) {
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON"} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	const schema = `
CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY, repo TEXT NOT NULL, trunk_branch TEXT NOT NULL DEFAULT '', created_ms INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS agents(id TEXT NOT NULL, session TEXT NOT NULL, model TEXT NOT NULL, pubkey BLOB NOT NULL, current_version INTEGER NOT NULL DEFAULT 1, state TEXT NOT NULL DEFAULT 'active', PRIMARY KEY(session,id));
CREATE TABLE IF NOT EXISTS agent_keys(session TEXT NOT NULL, agent TEXT NOT NULL, version INTEGER NOT NULL, pubkey BLOB NOT NULL, active INTEGER NOT NULL DEFAULT 1, revoked_ms INTEGER NOT NULL DEFAULT 0, created_ms INTEGER NOT NULL, PRIMARY KEY(session,agent,version));
CREATE TABLE IF NOT EXISTS events(seq INTEGER PRIMARY KEY, session TEXT NOT NULL, agent TEXT NOT NULL, type TEXT NOT NULL, payload TEXT NOT NULL, ts INTEGER NOT NULL, prev_hash TEXT NOT NULL, request_digest TEXT NOT NULL, signature BLOB NOT NULL, key_version INTEGER NOT NULL DEFAULT 1, hash TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS events_by_session ON events(session,seq);
CREATE TABLE IF NOT EXISTS leases(session TEXT NOT NULL, path TEXT NOT NULL, agent TEXT NOT NULL, intent TEXT NOT NULL, expires_ms INTEGER NOT NULL, PRIMARY KEY(session,path));
CREATE TABLE IF NOT EXISTS nonces(agent TEXT NOT NULL, nonce TEXT NOT NULL, seen_ms INTEGER NOT NULL, PRIMARY KEY(agent,nonce));
CREATE TABLE IF NOT EXISTS worktrees(session TEXT NOT NULL, agent TEXT NOT NULL, path TEXT NOT NULL, branch TEXT NOT NULL, PRIMARY KEY(session,agent));
CREATE TABLE IF NOT EXISTS queue(id INTEGER PRIMARY KEY AUTOINCREMENT, session TEXT NOT NULL, agent TEXT NOT NULL, commit_sha TEXT NOT NULL, branch TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'pending', reason TEXT NOT NULL DEFAULT '', created_ms INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS queue_pending ON queue(session,state,id);
CREATE TABLE IF NOT EXISTS reviews(queue_id INTEGER NOT NULL, reviewer TEXT NOT NULL, verdict TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', created_ms INTEGER NOT NULL, PRIMARY KEY(queue_id,reviewer));`
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	hadKeyVersion, err := hasColumn(db, "events", "key_version")
	if err != nil {
		db.Close()
		return nil, err
	}
	// Upgrade databases created by the first MVP release.
	for _, col := range []struct{ table, name, ddl string }{
		{"sessions", "trunk_branch", "TEXT NOT NULL DEFAULT ''"},
		{"agents", "current_version", "INTEGER NOT NULL DEFAULT 1"},
		{"agents", "state", "TEXT NOT NULL DEFAULT 'active'"},
		{"events", "key_version", "INTEGER NOT NULL DEFAULT 1"},
	} {
		if err := ensureColumn(db, col.table, col.name, col.ddl); err != nil {
			db.Close()
			return nil, err
		}
	}
	if !hadKeyVersion {
		if err := rehashLegacyEvents(db); err != nil {
			db.Close()
			return nil, err
		}
	}
	if _, err = db.Exec(`INSERT OR IGNORE INTO agent_keys(session,agent,version,pubkey,active,revoked_ms,created_ms) SELECT session,id,1,pubkey,1,0,0 FROM agents`); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}
func ensureColumn(db *sql.DB, table, name, ddl string) error {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notnull, pk int
		var col, typ string
		var def any
		if err = rows.Scan(&cid, &col, &typ, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		if col == name {
			found = true
		}
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if !found {
		_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + name + ` ` + ddl)
	}
	return err
}
func hasColumn(db *sql.DB, table, name string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var col, typ string
		var def any
		if err = rows.Scan(&cid, &col, &typ, &notnull, &def, &pk); err != nil {
			return false, err
		}
		if col == name {
			return true, nil
		}
	}
	return false, rows.Err()
}
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) CreateSession(id, repo string) error { return s.CreateSessionWithTrunk(id, repo, "") }
func (s *Store) CreateSessionWithTrunk(id, repo, trunk string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("session id is required")
	}
	_, err := s.db.Exec(`INSERT INTO sessions(id,repo,trunk_branch,created_ms) VALUES(?,?,?,?)`, id, repo, trunk, time.Now().UnixMilli())
	return err
}
func (s *Store) Session(id string) (string, error) {
	var repo string
	err := s.db.QueryRow(`SELECT repo FROM sessions WHERE id=?`, id).Scan(&repo)
	return repo, err
}
func (s *Store) SessionInfo(id string) (SessionInfo, error) {
	var x SessionInfo
	x.ID = id
	err := s.db.QueryRow(`SELECT repo,trunk_branch FROM sessions WHERE id=?`, id).Scan(&x.Repo, &x.TrunkBranch)
	return x, err
}
func (s *Store) Register(session, id, model string, pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("invalid Ed25519 public key")
	}
	if _, err := s.Session(session); err != nil {
		return fmt.Errorf("unknown session: %w", err)
	}
	now := time.Now().UnixMilli()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO agents(id,session,model,pubkey,current_version,state) VALUES(?,?,?, ?,1,'active')`, id, session, model, []byte(pub)); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO agent_keys(session,agent,version,pubkey,active,created_ms) VALUES(?,?,1,?,1,?)`, session, id, []byte(pub), now); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Agent(session, id string) (Agent, error) {
	var a Agent
	a.Session = session
	a.ID = id
	var active int
	var state string
	err := s.db.QueryRow(`SELECT a.model,a.current_version,a.state,k.pubkey,k.active FROM agents a JOIN agent_keys k ON k.session=a.session AND k.agent=a.id AND k.version=a.current_version WHERE a.session=? AND a.id=?`, session, id).Scan(&a.Model, &a.KeyVersion, &state, &a.Pubkey, &active)
	if err == nil {
		a.KeyRevoked = state != "active" || active == 0
	}
	return a, err
}
func (s *Store) RotateKey(session, id string, pub ed25519.PublicKey) (int, error) {
	return s.RotateKeyFrom(session, id, 0, pub)
}
func (s *Store) RotateKeyFrom(session, id string, expectedVersion int, pub ed25519.PublicKey) (int, error) {
	if len(pub) != ed25519.PublicKeySize {
		return 0, errors.New("invalid Ed25519 public key")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var version int
	var state string
	if err = tx.QueryRow(`SELECT current_version,state FROM agents WHERE session=? AND id=?`, session, id).Scan(&version, &state); err != nil {
		return 0, err
	}
	if expectedVersion > 0 && version != expectedVersion {
		return 0, ErrStaleKey
	}
	if state != "active" {
		return 0, ErrRevoked
	}
	version++
	if _, err = tx.Exec(`INSERT INTO agent_keys(session,agent,version,pubkey,active,created_ms) VALUES(?,?,?, ?,1,?)`, session, id, version, []byte(pub), time.Now().UnixMilli()); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(`UPDATE agents SET current_version=?,pubkey=? WHERE session=? AND id=?`, version, []byte(pub), session, id); err != nil {
		return 0, err
	}
	return version, tx.Commit()
}
func (s *Store) RevokeKey(session, id string, version int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int
	if err = tx.QueryRow(`SELECT current_version FROM agents WHERE session=? AND id=?`, session, id).Scan(&current); err != nil {
		return err
	}
	if version == current {
		return errors.New("cannot revoke the current key; rotate first")
	}
	res, err := tx.Exec(`UPDATE agent_keys SET active=0,revoked_ms=? WHERE session=? AND agent=? AND version=? AND active=1`, time.Now().UnixMilli(), session, id, version)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("key version not found or already revoked")
	}
	return tx.Commit()
}
func (s *Store) AgentKey(session, id string, version int) (ed25519.PublicKey, bool, error) {
	var pub []byte
	var active int
	err := s.db.QueryRow(`SELECT pubkey,active FROM agent_keys WHERE session=? AND agent=? AND version=?`, session, id, version).Scan(&pub, &active)
	return ed25519.PublicKey(pub), active == 1, err
}
func (s *Store) UseNonce(agent, nonce string, now int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM nonces WHERE seen_ms < ?`, now-10*60*1000); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO nonces(agent,nonce,seen_ms) VALUES(?,?,?)`, agent, nonce, now); err != nil {
		return err
	}
	return tx.Commit()
}

func eventHash(e Event) (string, error) {
	pre := struct {
		Seq           int64           `json:"seq"`
		Session       string          `json:"session"`
		Agent         string          `json:"agent"`
		Type          string          `json:"type"`
		Payload       json.RawMessage `json:"payload"`
		TS            int64           `json:"ts"`
		PrevHash      string          `json:"prev_hash"`
		RequestDigest string          `json:"request_digest"`
		Signature     []byte          `json:"signature"`
		KeyVersion    int             `json:"key_version"`
	}{e.Seq, e.Session, e.Agent, e.Type, e.Payload, e.TS, e.PrevHash, e.RequestDigest, e.Signature, e.KeyVersion}
	b, err := json.Marshal(pre)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func rehashLegacyEvents(db *sql.DB) error {
	rows, err := db.Query(`SELECT seq,session,agent,type,payload,ts,prev_hash,request_digest,signature,hash FROM events ORDER BY seq`)
	if err != nil {
		return err
	}
	events := []Event{}
	for rows.Next() {
		var e Event
		var payload string
		if err = rows.Scan(&e.Seq, &e.Session, &e.Agent, &e.Type, &payload, &e.TS, &e.PrevHash, &e.RequestDigest, &e.Signature, &e.Hash); err != nil {
			rows.Close()
			return err
		}
		e.Payload = json.RawMessage(payload)
		e.KeyVersion = 1
		events = append(events, e)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	prev := "genesis"
	for i := range events {
		events[i].PrevHash = prev
		events[i].Hash, err = eventHash(events[i])
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE events SET prev_hash=?,key_version=1,hash=? WHERE seq=?`, events[i].PrevHash, events[i].Hash, events[i].Seq); err != nil {
			return err
		}
		prev = events[i].Hash
	}
	return tx.Commit()
}
func (s *Store) Append(session, agent, typ string, payload any, digest string, sig []byte) (Event, error) {
	return s.AppendVersion(session, agent, typ, payload, digest, sig, 1)
}
func (s *Store) AppendVersion(session, agent, typ string, payload any, digest string, sig []byte, keyVersion int) (Event, error) {
	p, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback()
	var prev string
	err = tx.QueryRow(`SELECT hash FROM events ORDER BY seq DESC LIMIT 1`).Scan(&prev)
	if errors.Is(err, sql.ErrNoRows) {
		prev = "genesis"
	} else if err != nil {
		return Event{}, err
	}
	var seq int64
	if err = tx.QueryRow(`SELECT COALESCE(MAX(seq),0)+1 FROM events`).Scan(&seq); err != nil {
		return Event{}, err
	}
	e := Event{Seq: seq, Session: session, Agent: agent, Type: typ, Payload: p, TS: time.Now().UnixMilli(), PrevHash: prev, RequestDigest: digest, Signature: append([]byte(nil), sig...), KeyVersion: keyVersion}
	e.Hash, err = eventHash(e)
	if err != nil {
		return Event{}, err
	}
	_, err = tx.Exec(`INSERT INTO events(seq,session,agent,type,payload,ts,prev_hash,request_digest,signature,key_version,hash) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, e.Seq, e.Session, e.Agent, e.Type, string(e.Payload), e.TS, e.PrevHash, e.RequestDigest, e.Signature, e.KeyVersion, e.Hash)
	if err != nil {
		return Event{}, err
	}
	if err = tx.Commit(); err != nil {
		return Event{}, err
	}
	return e, nil
}

func cleanPaths(paths []string) ([]string, error) {
	set := map[string]bool{}
	for _, p := range paths {
		p = strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
		if p == "" || strings.HasPrefix(p, "/") {
			return nil, fmt.Errorf("invalid repository path %q", p)
		}
		p = path.Clean(p)
		if p == "." || p == ".." || strings.HasPrefix(p, "../") {
			return nil, fmt.Errorf("path escapes repository: %q", p)
		}
		set[p] = true
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}
func pathsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
func leaseCovers(lease, target string) bool {
	return target == lease || strings.HasPrefix(target, lease+"/")
}
func (s *Store) Claim(session, agent, intent string, paths []string, ttl time.Duration) error {
	paths, err := cleanPaths(paths)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return errors.New("at least one path is required")
	}
	if ttl <= 0 {
		return errors.New("lease TTL must be positive")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now, exp := time.Now().UnixMilli(), time.Now().Add(ttl).UnixMilli()
	rows, err := tx.Query(`SELECT path,agent,expires_ms FROM leases WHERE session=?`, session)
	if err != nil {
		return err
	}
	type existing struct {
		p, a string
		e    int64
	}
	var leases []existing
	for rows.Next() {
		var x existing
		if err = rows.Scan(&x.p, &x.a, &x.e); err != nil {
			rows.Close()
			return err
		}
		leases = append(leases, x)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, p := range paths {
		for _, x := range leases {
			if x.e > now && x.a != agent && pathsOverlap(p, x.p) {
				return fmt.Errorf("%w: %s overlaps %s (owner %s)", ErrLeaseHeld, p, x.p, x.a)
			}
		}
	}
	for _, p := range paths {
		if _, err = tx.Exec(`INSERT INTO leases(session,path,agent,intent,expires_ms) VALUES(?,?,?,?,?) ON CONFLICT(session,path) DO UPDATE SET agent=excluded.agent,intent=excluded.intent,expires_ms=excluded.expires_ms`, session, p, agent, intent, exp); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) LeaseAllows(session, agent, p string) (bool, error) {
	paths, err := cleanPaths([]string{p})
	if err != nil {
		return false, err
	}
	rows, err := s.db.Query(`SELECT path FROM leases WHERE session=? AND agent=? AND expires_ms>?`, session, agent, time.Now().UnixMilli())
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var owned string
		if err = rows.Scan(&owned); err != nil {
			return false, err
		}
		if leaseCovers(owned, paths[0]) {
			return true, nil
		}
	}
	return false, rows.Err()
}
func (s *Store) Renew(session, agent string, ttl time.Duration) (int64, error) {
	if ttl <= 0 {
		return 0, errors.New("lease TTL must be positive")
	}
	exp := time.Now().Add(ttl).UnixMilli()
	res, err := s.db.Exec(`UPDATE leases SET expires_ms=? WHERE session=? AND agent=? AND expires_ms>?`, exp, session, agent, time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return 0, ErrNotOwner
	}
	return exp, nil
}
func (s *Store) Release(session, agent string, paths []string) error {
	paths, err := cleanPaths(paths)
	if err != nil {
		return err
	}
	for _, p := range paths {
		if _, err = s.db.Exec(`DELETE FROM leases WHERE session=? AND path=? AND agent=?`, session, p, agent); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) Leases(session string) ([]map[string]any, error) {
	rows, err := s.db.Query(`SELECT path,agent,intent,expires_ms FROM leases WHERE session=? AND expires_ms>? ORDER BY path`, session, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var p, a, i string
		var exp int64
		if err = rows.Scan(&p, &a, &i, &exp); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"path": p, "agent": a, "intent": i, "expires_ms": exp})
	}
	return out, rows.Err()
}

func (s *Store) SetWorktree(session, agent, path, branch string) error {
	_, err := s.db.Exec(`INSERT INTO worktrees(session,agent,path,branch) VALUES(?,?,?,?) ON CONFLICT(session,agent) DO UPDATE SET path=excluded.path,branch=excluded.branch`, session, agent, path, branch)
	return err
}
func (s *Store) AgentWorktree(session, agent string) (Worktree, error) {
	var w Worktree
	w.Session = session
	w.Agent = agent
	err := s.db.QueryRow(`SELECT path,branch FROM worktrees WHERE session=? AND agent=?`, session, agent).Scan(&w.Path, &w.Branch)
	return w, err
}
func (s *Store) Enqueue(session, agent, sha, branch string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO queue(session,agent,commit_sha,branch,created_ms) VALUES(?,?,?,?,?)`, session, agent, sha, branch, time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
func (s *Store) QueueItem(id int64) (QueueItem, error) {
	var q QueueItem
	err := s.db.QueryRow(`SELECT id,session,agent,commit_sha,branch,state,reason,created_ms FROM queue WHERE id=?`, id).Scan(&q.ID, &q.Session, &q.Agent, &q.Commit, &q.Branch, &q.State, &q.Reason, &q.CreatedMS)
	return q, err
}
func (s *Store) Queue(session string) ([]QueueItem, error) {
	rows, err := s.db.Query(`SELECT id,session,agent,commit_sha,branch,state,reason,created_ms FROM queue WHERE session=? ORDER BY id`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QueueItem{}
	for rows.Next() {
		var q QueueItem
		if err = rows.Scan(&q.ID, &q.Session, &q.Agent, &q.Commit, &q.Branch, &q.State, &q.Reason, &q.CreatedMS); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
func (s *Store) HasReceipt(session, agent, commit string) (bool, error) {
	rows, err := s.db.Query(`SELECT payload FROM events WHERE session=? AND agent=? AND type='RECEIPT' ORDER BY seq DESC`, session, agent)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return false, err
		}
		var receipt struct {
			Commit string `json:"commit"`
		}
		if err = json.Unmarshal([]byte(raw), &receipt); err != nil {
			return false, err
		}
		if receipt.Commit == commit {
			return true, nil
		}
	}
	return false, rows.Err()
}
func (s *Store) Review(session string, queueID int64, reviewer, verdict, reason string) error {
	q, err := s.QueueItem(queueID)
	if err != nil {
		return err
	}
	if q.Session != session {
		return ErrQueueSession
	}
	if q.Agent == reviewer {
		return errors.New("author cannot review their own change")
	}
	if verdict != "approve" && verdict != "request_changes" {
		return errors.New("verdict must be approve or request_changes")
	}
	_, err = s.db.Exec(`INSERT INTO reviews(queue_id,reviewer,verdict,reason,created_ms) VALUES(?,?,?,?,?) ON CONFLICT(queue_id,reviewer) DO UPDATE SET verdict=excluded.verdict,reason=excluded.reason,created_ms=excluded.created_ms`, queueID, reviewer, verdict, reason, time.Now().UnixMilli())
	return err
}
func (s *Store) HasIndependentApproval(queueID int64, author string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM reviews WHERE queue_id=? AND reviewer<>? AND verdict='approve'`, queueID, author).Scan(&n)
	return n > 0, err
}
func (s *Store) SetQueueState(id int64, state, reason string) error {
	_, err := s.db.Exec(`UPDATE queue SET state=?,reason=? WHERE id=?`, state, reason, id)
	return err
}

func (s *Store) Events(session string) ([]Event, error) {
	rows, err := s.db.Query(`SELECT seq,session,agent,type,payload,ts,prev_hash,request_digest,signature,key_version,hash FROM events WHERE session=? ORDER BY seq`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var p string
		if err = rows.Scan(&e.Seq, &e.Session, &e.Agent, &e.Type, &p, &e.TS, &e.PrevHash, &e.RequestDigest, &e.Signature, &e.KeyVersion, &e.Hash); err != nil {
			return nil, err
		}
		e.Payload = json.RawMessage(p)
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) AllEvents() ([]Event, error) {
	rows, err := s.db.Query(`SELECT seq,session,agent,type,payload,ts,prev_hash,request_digest,signature,key_version,hash FROM events ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var p string
		if err = rows.Scan(&e.Seq, &e.Session, &e.Agent, &e.Type, &p, &e.TS, &e.PrevHash, &e.RequestDigest, &e.Signature, &e.KeyVersion, &e.Hash); err != nil {
			return nil, err
		}
		e.Payload = json.RawMessage(p)
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) Verify() (int, error) {
	rows, err := s.db.Query(`SELECT e.seq,e.session,e.agent,e.type,e.payload,e.ts,e.prev_hash,e.request_digest,e.signature,e.key_version,e.hash,k.pubkey FROM events e LEFT JOIN agent_keys k ON k.session=e.session AND k.agent=e.agent AND k.version=e.key_version ORDER BY e.seq`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	prev := "genesis"
	n := 0
	for rows.Next() {
		var e Event
		var p string
		var pub []byte
		if err = rows.Scan(&e.Seq, &e.Session, &e.Agent, &e.Type, &p, &e.TS, &e.PrevHash, &e.RequestDigest, &e.Signature, &e.KeyVersion, &e.Hash, &pub); err != nil {
			return n, err
		}
		e.Payload = json.RawMessage(p)
		if e.PrevHash != prev {
			return n, fmt.Errorf("event %d: previous hash mismatch", e.Seq)
		}
		h, err := eventHash(e)
		if err != nil {
			return n, err
		}
		if h != e.Hash {
			return n, fmt.Errorf("event %d: hash mismatch", e.Seq)
		}
		digest, err := hex.DecodeString(e.RequestDigest)
		if err != nil || len(pub) != ed25519.PublicKeySize || len(digest) != sha256.Size || !ed25519.Verify(ed25519.PublicKey(pub), digest, e.Signature) {
			return n, fmt.Errorf("event %d: invalid signature or key", e.Seq)
		}
		prev = e.Hash
		n++
	}
	return n, rows.Err()
}
