package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	URL     string `json:"url"`
	Session string `json:"session"`
	Repo    string `json:"repo"`
}
type Client struct {
	URL, Session, Agent string
	Private             ed25519.PrivateKey
	HTTP                *http.Client
}

func GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	return pub, priv, err
}
func SaveKey(file string, priv ed25519.PrivateKey) error {
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	return os.WriteFile(file, priv, 0600)
}
func LoadKey(file string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	if len(b) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid key file %s", file)
	}
	return ed25519.PrivateKey(b), nil
}
func ConfigPath(repo string) string { return filepath.Join(repo, ".collab", "config.json") }
func SaveConfig(repo string, c Config) error {
	if err := os.MkdirAll(filepath.Join(repo, ".collab"), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(repo), append(b, '\n'), 0600)
}
func LoadConfig(repo string) (Config, error) {
	b, err := os.ReadFile(ConfigPath(repo))
	if err != nil {
		return Config{}, err
	}
	var c Config
	err = json.Unmarshal(b, &c)
	return c, err
}
func FindRepo(start string) (string, error) {
	p, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err = os.Stat(ConfigPath(p)); err == nil {
			return p, nil
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	return "", fmt.Errorf("no .collab/config.json found; run collaborate init first")
}
func New(url, session, agent string, priv ed25519.PrivateKey) *Client {
	return &Client{URL: strings.TrimRight(url, "/"), Session: session, Agent: agent, Private: priv, HTTP: &http.Client{Timeout: 20 * time.Second}}
}
func (c *Client) Do(method, path string, body any) ([]byte, error) {
	var data []byte
	var err error
	if body == nil {
		data = []byte("{}")
	} else {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	ts := fmt.Sprint(time.Now().UnixMilli())
	nonceBytes := make([]byte, 16)
	if _, err = rand.Read(nonceBytes); err != nil {
		return nil, err
	}
	nonce := hex.EncodeToString(nonceBytes)
	h := sha256.New()
	for _, part := range [][]byte{[]byte(method), []byte{0}, []byte(path), []byte{0}, []byte(ts), []byte{0}, []byte(nonce), []byte{0}, data} {
		_, _ = h.Write(part)
	}
	digest := h.Sum(nil)
	sig := ed25519.Sign(c.Private, digest)
	req, err := http.NewRequest(method, c.URL+path, strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Collab-Agent", c.Agent)
	req.Header.Set("X-Collab-Session", c.Session)
	req.Header.Set("X-Collab-Timestamp", ts)
	req.Header.Set("X-Collab-Nonce", nonce)
	req.Header.Set("X-Collab-Signature", hex.EncodeToString(sig))
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("broker %s: %s", resp.Status, strings.TrimSpace(string(out)))
	}
	return out, nil
}
func Public(priv ed25519.PrivateKey) ed25519.PublicKey { return priv.Public().(ed25519.PublicKey) }
