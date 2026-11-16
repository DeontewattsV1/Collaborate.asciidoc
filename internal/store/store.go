package store

import (
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrLeaseHeld = errors.New("path is leased by another agent")
	ErrNotOwner  = errors.New("agent does not own an active lease")
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
	Hash          string          `json:"hash"`
}

type Agent struct {
	ID      string
	Session string
	Model   string
	Pubkey  ed25519.PublicKey
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
CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY, repo TEXT NOT NULL, created_ms INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS agents(id TEXT NOT NULL, session TEXT NOT NULL, model TEXT NOT NULL, pubkey BLOB NOT NULL, PRIMARY KEY(session,id));
CREATE TABLE IF NOT EXISTS events(seq INTEGER PRIMARY KEY, session TEXT NOT NULL, agent TEXT NOT NULL, type TEXT NOT NULL, payload TEXT NOT NULL, ts INTEGER NOT NULL, prev_hash TEXT NOT NULL, request_digest TEXT NOT NULL, signature BLOB NOT NULL, hash TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS events_by_session ON events(session,seq);
CREATE TABLE IF NOT EXISTS leases(session TEXT NOT NULL, path TEXT NOT NULL, agent TEXT NOT NULL, intent TEXT NOT NULL, expires_ms INTEGER NOT NULL, PRIMARY KEY(session,path));
CREATE TABLE IF NOT EXISTS nonces(agent TEXT NOT NULL, nonce TEXT NOT NULL, seen_ms INTEGER NOT NULL, PRIMARY KEY(agent,nonce));`
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) CreateSession(id, repo string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("session id is required")
	}
	_, err := s.db.Exec(`INSERT INTO sessions(id,repo,created_ms) VALUES(?,?,?)`, id, repo, time.Now().UnixMilli())
	return err
}
func (s *Store) Session(id string) (string, error) {
	var repo string
	err := s.db.QueryRow(`SELECT repo FROM sessions WHERE id=?`, id).Scan(&repo)
	return repo, err
}
func (s *Store) Register(session, id, model string, pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("invalid Ed25519 public key")
	}
	if _, err := s.Session(session); err != nil {
		return fmt.Errorf("unknown session: %w", err)
	}
	_, err := s.db.Exec(`INSERT INTO agents(session,id,model,pubkey) VALUES(?,?,?,?)`, session, id, model, []byte(pub))
	return err
}
func (s *Store) Agent(session, id string) (Agent, error) {
	var a Agent
	a.Session, a.ID = session, id
	err := s.db.QueryRow(`SELECT model,pubkey FROM agents WHERE session=? AND id=?`, session, id).Scan(&a.Model, &a.Pubkey)
	return a, err
}

// UseNonce makes signed requests one-shot and prevents replay within the timestamp window.
func (s *Store) UseNonce(agent, nonce string, now int64) error {
	_, err := s.db.Exec(`INSERT INTO nonces(agent,nonce,seen_ms) VALUES(?,?,?)`, agent, nonce, now)
	return err
}

func eventHash(e Event) (string, error) {
	preimage := struct {
		Seq           int64           `json:"seq"`
		Session       string          `json:"session"`
		Agent         string          `json:"agent"`
		Type          string          `json:"type"`
		Payload       json.RawMessage `json:"payload"`
		TS            int64           `json:"ts"`
		PrevHash      string          `json:"prev_hash"`
		RequestDigest string          `json:"request_digest"`
		Signature     []byte          `json:"signature"`
	}{e.Seq, e.Session, e.Agent, e.Type, e.Payload, e.TS, e.PrevHash, e.RequestDigest, e.Signature}
	b, err := json.Marshal(preimage)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func (s *Store) Append(session, agent, typ string, payload any, digest string, sig []byte) (Event, error) {
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
	e := Event{Seq: seq, Session: session, Agent: agent, Type: typ, Payload: p, TS: time.Now().UnixMilli(), PrevHash: prev, RequestDigest: digest, Signature: append([]byte(nil), sig...)}
	e.Hash, err = eventHash(e)
	if err != nil {
		return Event{}, err
	}
	_, err = tx.Exec(`INSERT INTO events(seq,session,agent,type,payload,ts,prev_hash,request_digest,signature,hash) VALUES(?,?,?,?,?,?,?,?,?,?)`, e.Seq, e.Session, e.Agent, e.Type, string(e.Payload), e.TS, e.PrevHash, e.RequestDigest, e.Signature, e.Hash)
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
		parts := strings.Split(p, "/")
		for _, part := range parts {
			if part == ".." {
				return nil, fmt.Errorf("path escapes repository: %q", p)
			}
		}
		p = strings.TrimPrefix(p, "./")
		if p == "." {
			return nil, errors.New("path must name a file or directory")
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
func (s *Store) Claim(session, agent, intent string, paths []string, ttl time.Duration) error {
	paths, err := cleanPaths(paths)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return errors.New("at least one path is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now, exp := time.Now().UnixMilli(), time.Now().Add(ttl).UnixMilli()
	for _, p := range paths {
		var owner string
		var until int64
		err := tx.QueryRow(`SELECT agent,expires_ms FROM leases WHERE session=? AND path=?`, session, p).Scan(&owner, &until)
		if err == nil && until > now && owner != agent {
			return fmt.Errorf("%w: %s (owner %s)", ErrLeaseHeld, p, owner)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO leases(session,path,agent,intent,expires_ms) VALUES(?,?,?,?,?) ON CONFLICT(session,path) DO UPDATE SET agent=excluded.agent,intent=excluded.intent,expires_ms=excluded.expires_ms`, session, p, agent, intent, exp); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Renew(session, agent string, ttl time.Duration) (int64, error) {
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
		if err := rows.Scan(&p, &a, &i, &exp); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"path": p, "agent": a, "intent": i, "expires_ms": exp})
	}
	return out, rows.Err()
}
func (s *Store) Events(session string) ([]Event, error) {
	rows, err := s.db.Query(`SELECT seq,session,agent,type,payload,ts,prev_hash,request_digest,signature,hash FROM events WHERE session=? ORDER BY seq`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var p string
		if err := rows.Scan(&e.Seq, &e.Session, &e.Agent, &e.Type, &p, &e.TS, &e.PrevHash, &e.RequestDigest, &e.Signature, &e.Hash); err != nil {
			return nil, err
		}
		e.Payload = json.RawMessage(p)
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) Verify() (int, error) {
	rows, err := s.db.Query(`SELECT e.seq,e.session,e.agent,e.type,e.payload,e.ts,e.prev_hash,e.request_digest,e.signature,e.hash,a.pubkey FROM events e LEFT JOIN agents a ON a.session=e.session AND a.id=e.agent ORDER BY e.seq`)
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
		if err := rows.Scan(&e.Seq, &e.Session, &e.Agent, &e.Type, &p, &e.TS, &e.PrevHash, &e.RequestDigest, &e.Signature, &e.Hash, &pub); err != nil {
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
		if len(pub) != ed25519.PublicKeySize {
			return n, fmt.Errorf("event %d: unknown signer", e.Seq)
		}
		digest, err := hex.DecodeString(e.RequestDigest)
		if err != nil || len(digest) != sha256.Size || !ed25519.Verify(ed25519.PublicKey(pub), digest, e.Signature) {
			return n, fmt.Errorf("event %d: invalid signature", e.Seq)
		}
		prev = e.Hash
		n++
	}
	return n, rows.Err()
}
