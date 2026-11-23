package agentterm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SessionFile is an agent's local clock-in state. It holds the session's
// private key, so it is written with mode 0600 and must never be committed.
type SessionFile struct {
	Server  string `json:"server"`
	Session string `json:"session"`
	Agent   string `json:"agent"`
	Seed    string `json:"seed"`
	Nonce   int64  `json:"nonce"`
}

// Client talks to a terminal server.
type Client struct {
	Server    string
	ReadToken string
	HTTP      *http.Client
	Path      string // session file path
	mu        sync.Mutex
	sess      *SessionFile
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// ClockIn registers a fresh session key using a GitHub token. A previous
// session of the same agent instance is superseded (rejoin), keeping claims.
func (c *Client) ClockIn(ctx context.Context, githubToken, instance, branch string) (*SessionFile, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	pubHex := hex.EncodeToString(pub)
	nonce := time.Now().UnixMilli()
	req := ClockInRequest{Instance: instance, Branch: branch, PubKey: pubHex, Nonce: nonce,
		Sig: hex.EncodeToString(ed25519.Sign(priv, ClockInMessage(instance, branch, pubHex, nonce)))}
	var resp struct {
		Session string `json:"session"`
		Agent   string `json:"agent"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/clock-in", "Bearer "+githubToken, req, &resp); err != nil {
		return nil, err
	}
	sf := &SessionFile{Server: c.Server, Session: resp.Session, Agent: resp.Agent, Seed: hex.EncodeToString(priv.Seed()), Nonce: nonce}
	c.mu.Lock()
	c.sess = sf
	c.mu.Unlock()
	return sf, c.save()
}

// Load reads the session file.
func (c *Client) Load() error {
	data, err := os.ReadFile(c.Path)
	if err != nil {
		return fmt.Errorf("not clocked in (%w)", err)
	}
	var sf SessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return err
	}
	c.mu.Lock()
	c.sess = &sf
	if c.Server == "" {
		c.Server = sf.Server
	}
	c.mu.Unlock()
	return nil
}

func (c *Client) save() error {
	if c.Path == "" {
		return nil
	}
	c.mu.Lock()
	data, _ := json.MarshalIndent(c.sess, "", "  ")
	c.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil {
		return err
	}
	tmp := c.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.Path)
}

// Post signs and sends one request. body must marshal to a JSON object.
func (c *Client) Post(ctx context.Context, kind string, body any) (Entry, error) {
	c.mu.Lock()
	if c.sess == nil {
		c.mu.Unlock()
		return Entry{}, errors.New("not clocked in")
	}
	seed, err := hex.DecodeString(c.sess.Seed)
	if err != nil || len(seed) != ed25519.SeedSize {
		c.mu.Unlock()
		return Entry{}, errors.New("session file has a bad key")
	}
	nonce := c.sess.Nonce + 1
	if now := time.Now().UnixMilli(); now > nonce {
		nonce = now
	}
	c.sess.Nonce = nonce
	session := c.sess.Session
	c.mu.Unlock()
	raw := MustBody(body)
	req := EntryRequest{Session: session, Kind: kind, Nonce: nonce, Body: raw,
		Sig: hex.EncodeToString(ed25519.Sign(ed25519.NewKeyFromSeed(seed), RequestMessage(session, kind, nonce, raw)))}
	if err := c.save(); err != nil {
		return Entry{}, err
	}
	var e Entry
	err = c.do(ctx, http.MethodPost, "/api/entries", "", req, &e)
	return e, err
}

// Get fetches a read endpoint into out.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, c.readAuth(), nil, out)
}

func (c *Client) readAuth() string {
	if c.ReadToken == "" {
		return ""
	}
	return "Bearer " + c.ReadToken
}

func (c *Client) do(ctx context.Context, method, path, auth string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, _ := json.Marshal(in)
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Server, "/")+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return fmt.Errorf("terminal refused (HTTP %d): %s", resp.StatusCode, e.Error)
		}
		return fmt.Errorf("terminal refused (HTTP %d)", resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Tail streams entries after since to fn, reconnecting and resuming from the
// last seen sequence until ctx ends. Each entry's link to its predecessor is
// checked as it arrives.
func (c *Client) Tail(ctx context.Context, since int64, fn func(Entry) error) error {
	prevHash := ""
	for ctx.Err() == nil {
		u := strings.TrimRight(c.Server, "/") + "/api/stream?since=" + strconv.FormatInt(since, 10)
		if c.ReadToken != "" {
			u += "&token=" + url.QueryEscape(c.ReadToken)
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
			continue
		}
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 1<<20), 4<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var e Entry
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e); err != nil {
				resp.Body.Close()
				return err
			}
			if ComputeHash(e) != e.Hash || (prevHash != "" && e.Prev != prevHash) {
				resp.Body.Close()
				return fmt.Errorf("entry %d failed hash or link verification", e.Seq)
			}
			prevHash, since = e.Hash, e.Seq
			if err := fn(e); err != nil {
				resp.Body.Close()
				return err
			}
		}
		resp.Body.Close()
	}
	return ctx.Err()
}
