package agentterm

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Terminal owns the log, the replayed state and the server key. All writes
// are serialized, validated by State.Apply and then durably appended (fsync)
// before they are visible to readers.
type Terminal struct {
	mu      sync.Mutex
	entries []Entry
	state   *State
	key     ed25519.PrivateKey
	file    *os.File
	changed chan struct{}
	broken  error
	now     func() time.Time
}

// Open loads (or creates) the JSONL log at logPath, verifying the full chain
// and replaying every entry. A log that fails verification is refused: the
// terminal fails closed rather than serve a history it cannot vouch for.
func Open(logPath string, key ed25519.PrivateKey, state *State, now func() time.Time) (*Terminal, error) {
	if now == nil {
		now = time.Now
	}
	t := &Terminal{state: state, key: key, changed: make(chan struct{}), now: now}
	if logPath != "" {
		if err := t.load(logPath); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		t.file = f
	}
	if len(t.entries) == 0 {
		if _, err := t.appendLocked(Entry{Kind: KindGenesis, Body: MustBody(map[string]string{
			"protocol": "agentterm/v1", "server_key": hex.EncodeToString(key.Public().(ed25519.PublicKey)),
		})}); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func (t *Terminal) load(logPath string) error {
	f, err := os.Open(logPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 4<<20)
	var entries []Entry
	for sc.Scan() {
		var e Entry
		dec := json.NewDecoder(bytes.NewReader(sc.Bytes()))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&e); err != nil {
			return fmt.Errorf("log line %d: %w", len(entries)+1, err)
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	if _, err := VerifyChain(entries, t.key.Public().(ed25519.PublicKey)); err != nil {
		return fmt.Errorf("refusing to load %s: %w", logPath, err)
	}
	for _, e := range entries {
		if err := t.state.Apply(e); err != nil {
			return fmt.Errorf("refusing to load %s: entry %d breaks a terminal rule: %w", logPath, e.Seq, err)
		}
	}
	t.entries = entries
	return nil
}

// Append validates, signs and stores a new entry. Seq, Prev, Time, Hash and
// ServerSig are always set by the server.
func (t *Terminal) Append(e Entry) (Entry, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.appendLocked(e)
}

func (t *Terminal) appendLocked(e Entry) (Entry, error) {
	if t.broken != nil {
		return Entry{}, fmt.Errorf("terminal is read-only after a storage failure: %w", t.broken)
	}
	e.Seq = int64(len(t.entries))
	e.Prev = ""
	if e.Seq > 0 {
		e.Prev = t.entries[e.Seq-1].Hash
	}
	now := t.now().UTC()
	if e.Seq > 0 {
		if last, err := time.Parse(time.RFC3339Nano, t.entries[e.Seq-1].Time); err == nil && now.Before(last) {
			now = last
		}
	}
	e.Time = now.Format(time.RFC3339Nano)
	e.Hash = ComputeHash(e)
	e.ServerSig = hex.EncodeToString(ed25519.Sign(t.key, []byte(e.Hash)))
	if err := t.state.Apply(e); err != nil {
		return Entry{}, err
	}
	if t.file != nil {
		line, _ := json.Marshal(e)
		if _, err := t.file.Write(append(line, '\n')); err != nil {
			t.broken = err
			return Entry{}, err
		}
		if err := t.file.Sync(); err != nil {
			t.broken = err
			return Entry{}, err
		}
	}
	t.entries = append(t.entries, e)
	close(t.changed)
	t.changed = make(chan struct{})
	return e, nil
}

// Since returns entries with seq > since, plus a channel closed on the next append.
func (t *Terminal) Since(since int64, limit int) ([]Entry, <-chan struct{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	start := since + 1
	if start < 0 {
		start = 0
	}
	if start > int64(len(t.entries)) {
		start = int64(len(t.entries))
	}
	end := int64(len(t.entries))
	if limit > 0 && end-start > int64(limit) {
		end = start + int64(limit)
	}
	out := append([]Entry(nil), t.entries[start:end]...)
	return out, t.changed
}

// Snapshot returns the current state view.
func (t *Terminal) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state.Snapshot(t.now())
}

// Verify re-verifies the complete in-memory chain.
func (t *Terminal) Verify() (VerifyReport, error) {
	t.mu.Lock()
	entries := append([]Entry(nil), t.entries...)
	t.mu.Unlock()
	return VerifyChain(entries, t.PublicKey())
}

// PublicKey is the server's Ed25519 verification key.
func (t *Terminal) PublicKey() ed25519.PublicKey { return t.key.Public().(ed25519.PublicKey) }

// session returns a copy of a session for request authentication.
func (t *Terminal) session(id string) (Session, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.state.Sessions[id]
	if !ok {
		return Session{}, false
	}
	return *s, true
}

// ExpireStale writes an expire entry for every session past its TTL.
func (t *Terminal) ExpireStale() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, id := range t.state.Expired(t.now()) {
		sess := t.state.Sessions[id]
		_, _ = t.appendLocked(Entry{Kind: KindExpire, Session: id, Agent: sess.Agent, Body: MustBody(map[string]string{"session": id})})
	}
}

// Close closes the log file.
func (t *Terminal) Close() error {
	if t.file != nil {
		return t.file.Close()
	}
	return nil
}

// LoadOrCreateKey reads a hex Ed25519 seed from path, creating one (0600) if absent.
func LoadOrCreateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		seed := make([]byte, ed25519.SeedSize)
		if _, err := rand.Read(seed); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(hex.EncodeToString(seed)+"\n"), 0o600); err != nil {
			return nil, err
		}
		return ed25519.NewKeyFromSeed(seed), nil
	}
	if err != nil {
		return nil, err
	}
	seed, err := hex.DecodeString(string(bytes.TrimSpace(data)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%s: not a hex Ed25519 seed", path)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}
