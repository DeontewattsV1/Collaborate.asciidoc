package collab

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"collaborate/internal/sqlite"
)

var agentPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)
var commitPattern = regexp.MustCompile(`^[a-fA-F0-9]{40}([a-fA-F0-9]{24})?$`)
var ErrConflict = errors.New("conflict")
var ErrInvalid = errors.New("invalid input")
var ErrNotFound = errors.New("not found")

type Event struct {
	Seq       int64           `json:"seq"`
	Timestamp string          `json:"timestamp"`
	Actor     string          `json:"actor"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	PrevHash  string          `json:"prev_hash"`
	Hash      string          `json:"hash"`
	Signature string          `json:"signature"`
}
type RegisterInput struct {
	Agent string `json:"agent"`
	Model string `json:"model"`
}
type MessageInput struct {
	Agent string `json:"agent"`
	To    string `json:"to"`
	Body  string `json:"body"`
}
type LeaseInput struct {
	Agent      string `json:"agent"`
	Path       string `json:"path"`
	TTLSeconds int64  `json:"ttl_seconds"`
}
type RenewInput struct {
	Agent      string `json:"agent"`
	LeaseID    string `json:"lease_id"`
	TTLSeconds int64  `json:"ttl_seconds"`
}
type ReleaseInput struct {
	Agent   string `json:"agent"`
	LeaseID string `json:"lease_id"`
}
type ReceiptInput struct {
	Agent   string   `json:"agent"`
	Commit  string   `json:"commit"`
	Paths   []string `json:"paths"`
	Summary string   `json:"summary"`
}
type GateInput struct {
	Agent        string   `json:"agent"`
	Commit       string   `json:"commit,omitempty"`
	Command      []string `json:"command"`
	ExitCode     int      `json:"exit_code"`
	OutputSHA256 string   `json:"output_sha256"`
	Worktree     string   `json:"worktree,omitempty"`
}
type Lease struct {
	ID        string `json:"id"`
	Agent     string `json:"agent"`
	Path      string `json:"path"`
	ExpiresAt string `json:"expires_at"`
}

type Store struct {
	db     *sqlite.DB
	key    ed25519.PrivateKey
	public ed25519.PublicKey
}

func OpenStore(dbPath, keyPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		return nil, err
	}
	priv, err := loadKey(keyPath)
	if err != nil {
		return nil, err
	}
	db, err := sqlite.Open(dbPath)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, key: priv, public: priv.Public().(ed25519.PublicKey)}
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS events (seq INTEGER PRIMARY KEY, ts TEXT NOT NULL, actor TEXT NOT NULL, kind TEXT NOT NULL, payload TEXT NOT NULL, prev_hash TEXT NOT NULL, hash TEXT NOT NULL, signature TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS agents (agent TEXT PRIMARY KEY, model TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS leases (id TEXT PRIMARY KEY, agent TEXT NOT NULL, path TEXT NOT NULL, expires INTEGER NOT NULL, active INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS leases_active ON leases(active,expires)`,
	} {
		if err := db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}
func (s *Store) Close() error                 { return s.db.Close() }
func (s *Store) PublicKey() ed25519.PublicKey { return append([]byte(nil), s.public...) }
func loadKey(p string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(p)
	if err == nil {
		if len(b) != ed25519.PrivateKeySize {
			return nil, errors.New("invalid broker key size")
		}
		return ed25519.PrivateKey(b), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		return loadKey(p)
	}
	if err != nil {
		return nil, err
	}
	if _, err = f.Write(key); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return key, nil
}
func validAgent(a string) error {
	if !agentPattern.MatchString(a) {
		return fmt.Errorf("%w: invalid agent", ErrInvalid)
	}
	return nil
}
func NormalizePath(p string) (string, error) {
	if p == "" || strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") || strings.Contains(p, ":") {
		return "", fmt.Errorf("%w: invalid relative path", ErrInvalid)
	}
	parts := strings.Split(p, "/")
	for _, v := range parts {
		if v == ".." || v == "." || v == "" {
			return "", fmt.Errorf("%w: unsafe path component", ErrInvalid)
		}
	}
	clean := path.Clean(p)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: unsafe path", ErrInvalid)
	}
	return clean, nil
}
func overlaps(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
func covered(leasePath, filePath string) bool {
	return leasePath == filePath || strings.HasPrefix(filePath, leasePath+"/")
}
func digest(e Event) string {
	msg := fmt.Sprintf("collab-event-v1\n%d\n%s\n%s\n%s\n%s\n%s\n", e.Seq, e.Timestamp, e.Actor, e.Type, string(e.Payload), e.PrevHash)
	h := sha256.Sum256([]byte(msg))
	return hex.EncodeToString(h[:])
}
func (s *Store) append(tx *sqlite.Tx, actor, kind string, payload any) (Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	prev := ""
	seq := int64(1)
	rows, err := tx.Query(`SELECT seq,hash FROM events ORDER BY seq DESC LIMIT 1`)
	if err != nil {
		return Event{}, err
	}
	if len(rows) > 0 {
		seq = sqlite.Int(rows[0], "seq") + 1
		prev = rows[0]["hash"]
	}
	e := Event{Seq: seq, Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Actor: actor, Type: kind, Payload: data, PrevHash: prev}
	e.Hash = digest(e)
	e.Signature = hex.EncodeToString(ed25519.Sign(s.key, []byte(e.Hash)))
	if err = tx.Exec(`INSERT INTO events(seq,ts,actor,kind,payload,prev_hash,hash,signature) VALUES(?,?,?,?,?,?,?,?)`, seq, e.Timestamp, e.Actor, e.Type, string(e.Payload), e.PrevHash, e.Hash, e.Signature); err != nil {
		return Event{}, err
	}
	return e, nil
}
func requireAgent(tx *sqlite.Tx, agent string) error {
	if err := validAgent(agent); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT agent FROM agents WHERE agent=?`, agent)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("%w: unregistered agent", ErrInvalid)
	}
	return nil
}
func (s *Store) Register(in RegisterInput) (Event, error) {
	if err := validAgent(in.Agent); err != nil {
		return Event{}, err
	}
	if len(in.Model) < 1 || len(in.Model) > 128 {
		return Event{}, fmt.Errorf("%w: model name", ErrInvalid)
	}
	var out Event
	err := s.db.Transaction(func(tx *sqlite.Tx) error {
		rows, err := tx.Query(`SELECT agent FROM agents WHERE agent=?`, in.Agent)
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			return fmt.Errorf("%w: agent already registered", ErrConflict)
		}
		if err := tx.Exec(`INSERT INTO agents(agent,model) VALUES(?,?)`, in.Agent, in.Model); err != nil {
			return err
		}
		out, err = s.append(tx, in.Agent, "REGISTER", in)
		return err
	})
	return out, err
}
func (s *Store) Say(in MessageInput) (Event, error) {
	if len(in.Body) == 0 || len(in.Body) > 4096 || len(in.To) > 64 {
		return Event{}, fmt.Errorf("%w: message size", ErrInvalid)
	}
	var out Event
	err := s.db.Transaction(func(tx *sqlite.Tx) error {
		if err := requireAgent(tx, in.Agent); err != nil {
			return err
		}
		var err error
		out, err = s.append(tx, in.Agent, "MESSAGE", in)
		return err
	})
	return out, err
}
func (s *Store) Claim(in LeaseInput) (Lease, Event, error) {
	var lease Lease
	var event Event
	if in.TTLSeconds == 0 {
		in.TTLSeconds = 60
	}
	if in.TTLSeconds < 5 || in.TTLSeconds > 3600 {
		return lease, event, fmt.Errorf("%w: ttl must be 5..3600", ErrInvalid)
	}
	p, err := NormalizePath(in.Path)
	if err != nil {
		return lease, event, err
	}
	in.Path = p
	expires := time.Now().Add(time.Duration(in.TTLSeconds) * time.Second)
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return lease, event, err
	}
	lease = Lease{ID: hex.EncodeToString(idBytes), Agent: in.Agent, Path: p, ExpiresAt: expires.UTC().Format(time.RFC3339Nano)}
	err = s.db.Transaction(func(tx *sqlite.Tx) error {
		if err := requireAgent(tx, in.Agent); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT path,agent FROM leases WHERE active=1 AND expires>?`, time.Now().Unix())
		if err != nil {
			return err
		}
		for _, r := range rows {
			if overlaps(r["path"], p) {
				return fmt.Errorf("%w: path held by %s", ErrConflict, r["agent"])
			}
		}
		if err := tx.Exec(`INSERT INTO leases(id,agent,path,expires,active)VALUES(?,?,?,?,1)`, lease.ID, in.Agent, p, expires.Unix()); err != nil {
			return err
		}
		event, err = s.append(tx, in.Agent, "CLAIM", lease)
		return err
	})
	return lease, event, err
}
func (s *Store) Renew(in RenewInput) (Lease, Event, error) {
	var lease Lease
	var out Event
	if in.TTLSeconds == 0 {
		in.TTLSeconds = 60
	}
	if in.TTLSeconds < 5 || in.TTLSeconds > 3600 {
		return lease, out, fmt.Errorf("%w: ttl must be 5..3600", ErrInvalid)
	}
	err := s.db.Transaction(func(tx *sqlite.Tx) error {
		if err := requireAgent(tx, in.Agent); err != nil {
			return err
		}
		now := time.Now().Unix()
		rows, err := tx.Query(`SELECT path FROM leases WHERE id=? AND agent=? AND active=1 AND expires>?`, in.LeaseID, in.Agent, now)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return fmt.Errorf("%w: lease missing, expired, or not owned", ErrNotFound)
		}
		exp := time.Now().Add(time.Duration(in.TTLSeconds) * time.Second)
		lease = Lease{ID: in.LeaseID, Agent: in.Agent, Path: rows[0]["path"], ExpiresAt: exp.UTC().Format(time.RFC3339Nano)}
		if err := tx.Exec(`UPDATE leases SET expires=? WHERE id=?`, exp.Unix(), in.LeaseID); err != nil {
			return err
		}
		out, err = s.append(tx, in.Agent, "RENEW", lease)
		return err
	})
	return lease, out, err
}
func (s *Store) Release(in ReleaseInput) (Event, error) {
	var out Event
	err := s.db.Transaction(func(tx *sqlite.Tx) error {
		if err := requireAgent(tx, in.Agent); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT id FROM leases WHERE id=? AND agent=? AND active=1`, in.LeaseID, in.Agent)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return fmt.Errorf("%w: lease not active or not owned", ErrNotFound)
		}
		if err := tx.Exec(`UPDATE leases SET active=0 WHERE id=?`, in.LeaseID); err != nil {
			return err
		}
		out, err = s.append(tx, in.Agent, "RELEASE", in)
		return err
	})
	return out, err
}
func (s *Store) Receipt(in ReceiptInput) (Event, error) {
	if !commitPattern.MatchString(in.Commit) || len(in.Summary) == 0 || len(in.Summary) > 2048 || len(in.Paths) == 0 || len(in.Paths) > 256 {
		return Event{}, fmt.Errorf("%w: receipt fields", ErrInvalid)
	}
	for i, p := range in.Paths {
		v, err := NormalizePath(p)
		if err != nil {
			return Event{}, err
		}
		in.Paths[i] = v
	}
	var out Event
	err := s.db.Transaction(func(tx *sqlite.Tx) error {
		if err := requireAgent(tx, in.Agent); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT path FROM leases WHERE agent=? AND active=1 AND expires>?`, in.Agent, time.Now().Unix())
		if err != nil {
			return err
		}
		for _, p := range in.Paths {
			ok := false
			for _, r := range rows {
				if covered(r["path"], p) {
					ok = true
					break
				}
			}
			if !ok {
				return fmt.Errorf("%w: no current lease for %s", ErrConflict, p)
			}
		}
		out, err = s.append(tx, in.Agent, "RECEIPT", in)
		return err
	})
	return out, err
}
func (s *Store) Gate(in GateInput) (Event, error) {
	if len(in.Command) == 0 || len(in.Command) > 64 || !regexp.MustCompile(`^[a-fA-F0-9]{64}$`).MatchString(in.OutputSHA256) {
		return Event{}, fmt.Errorf("%w: gate fields", ErrInvalid)
	}
	var out Event
	err := s.db.Transaction(func(tx *sqlite.Tx) error {
		if err := requireAgent(tx, in.Agent); err != nil {
			return err
		}
		var err error
		out, err = s.append(tx, in.Agent, "GATE_REPORTED", in)
		return err
	})
	return out, err
}
func (s *Store) Events(after int64) ([]Event, error) { return ReadEvents(s.db, after) }
func ReadEvents(db *sqlite.DB, after int64) ([]Event, error) {
	rows, err := db.Query(`SELECT seq,ts,actor,kind,payload,prev_hash,hash,signature FROM events WHERE seq>? ORDER BY seq`, after)
	if err != nil {
		return nil, err
	}
	result := make([]Event, 0, len(rows))
	for _, r := range rows {
		result = append(result, Event{Seq: sqlite.Int(r, "seq"), Timestamp: r["ts"], Actor: r["actor"], Type: r["kind"], Payload: json.RawMessage(r["payload"]), PrevHash: r["prev_hash"], Hash: r["hash"], Signature: r["signature"]})
	}
	return result, nil
}

// Verify checks every event and optionally the anchored expected head; it does not
// independently prove the agent's model identity, commands, or tests were genuine.
func Verify(dbPath string, pubKey ed25519.PublicKey, expectedHead string) (int, string, error) {
	if len(pubKey) != ed25519.PublicKeySize {
		return 0, "", errors.New("expected pinned Ed25519 public key")
	}
	db, err := sqlite.Open(dbPath)
	if err != nil {
		return 0, "", err
	}
	defer db.Close()
	events, err := ReadEvents(db, 0)
	if err != nil {
		return 0, "", err
	}
	prev := ""
	seq := int64(1)
	for _, e := range events {
		if e.Seq != seq || e.PrevHash != prev || digest(e) != e.Hash {
			return int(seq - 1), prev, fmt.Errorf("ledger broken at seq %d", seq)
		}
		sig, err := hex.DecodeString(e.Signature)
		if err != nil || !ed25519.Verify(pubKey, []byte(e.Hash), sig) {
			return int(seq - 1), prev, fmt.Errorf("signature invalid at seq %d", seq)
		}
		prev = e.Hash
		seq++
	}
	if expectedHead != "" && prev != expectedHead {
		return len(events), prev, fmt.Errorf("ledger head mismatch: expected %s got %s", expectedHead, prev)
	}
	return len(events), prev, nil
}
