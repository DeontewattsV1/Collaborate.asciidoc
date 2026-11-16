package broker

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"collaborate/internal/render"
	"collaborate/internal/store"
)

type Server struct {
	Store    *store.Store
	LeaseTTL time.Duration
}
type authInfo struct {
	session, agent, digest string
	sig                    []byte
}
type payload struct {
	Agent   string            `json:"agent"`
	Model   string            `json:"model"`
	Pubkey  ed25519.PublicKey `json:"pubkey"`
	Intent  string            `json:"intent"`
	Paths   []string          `json:"paths"`
	Body    string            `json:"body"`
	Commit  string            `json:"commit"`
	DiffSHA string            `json:"diff_sha"`
	Files   []string          `json:"files"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func requestDigest(method, path string, ts, nonce string, body []byte) []byte {
	h := sha256.New()
	for _, part := range [][]byte{[]byte(method), []byte{0}, []byte(path), []byte{0}, []byte(ts), []byte{0}, []byte(nonce), []byte{0}, body} {
		_, _ = h.Write(part)
	}
	return h.Sum(nil)
}
func (s *Server) authenticate(r *http.Request, body []byte) (authInfo, error) {
	agent := r.Header.Get("X-Collab-Agent")
	ts := r.Header.Get("X-Collab-Timestamp")
	nonce := r.Header.Get("X-Collab-Nonce")
	sigText := r.Header.Get("X-Collab-Signature")
	session := r.Header.Get("X-Collab-Session")
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
	if err != nil {
		return authInfo{}, fmt.Errorf("unknown agent/session")
	}
	d := requestDigest(r.Method, r.URL.RequestURI(), ts, nonce, body)
	if !ed25519.Verify(a.Pubkey, d, sig) {
		return authInfo{}, fmt.Errorf("signature verification failed")
	}
	if err = s.Store.UseNonce(agent, nonce, ms); err != nil {
		return authInfo{}, fmt.Errorf("replayed request nonce")
	}
	return authInfo{session: session, agent: agent, digest: hex.EncodeToString(d), sig: sig}, nil
}
func (s *Server) signed(typ string, fn func(http.ResponseWriter, *http.Request, authInfo, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": "cannot read request"})
			return
		}
		a, err := s.authenticate(r, body)
		if err != nil {
			writeJSON(w, 401, map[string]string{"error": err.Error()})
			return
		}
		_ = typ
		fn(w, r, a, body)
	}
}
func decode(body []byte, p *payload) error { return json.Unmarshal(body, p) }
func (s *Server) append(w http.ResponseWriter, a authInfo, typ string, data any) {
	e, err := s.Store.Append(a.session, a.agent, typ, data, a.digest, a.sig)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"seq": e.Seq, "hash": e.Hash})
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID   string `json:"id"`
			Repo string `json:"repo"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		if err := s.Store.CreateSession(in.ID, in.Repo); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 201, map[string]string{"session": in.ID})
	})
	mux.HandleFunc("POST /v1/agents", func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("session")
		var in payload
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		if in.Agent == "" || in.Model == "" {
			writeJSON(w, 400, map[string]string{"error": "agent and model are required"})
			return
		}
		if err := s.Store.Register(session, in.Agent, in.Model, in.Pubkey); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 201, map[string]string{"agent": in.Agent, "model": in.Model})
	})
	mux.HandleFunc("POST /v1/claim", s.signed("CLAIM", func(w http.ResponseWriter, r *http.Request, a authInfo, b []byte) {
		var in payload
		if err := decode(b, &in); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		if err := s.Store.Claim(a.session, a.agent, in.Intent, in.Paths, s.LeaseTTL); err != nil {
			writeJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}
		s.append(w, a, "CLAIM", map[string]any{"intent": in.Intent, "paths": in.Paths})
	}))
	mux.HandleFunc("POST /v1/release", s.signed("RELEASE", func(w http.ResponseWriter, r *http.Request, a authInfo, b []byte) {
		var in payload
		if err := decode(b, &in); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		if err := s.Store.Release(a.session, a.agent, in.Paths); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		s.append(w, a, "RELEASE", map[string]any{"paths": in.Paths})
	}))
	mux.HandleFunc("POST /v1/renew", s.signed("RENEW", func(w http.ResponseWriter, r *http.Request, a authInfo, b []byte) {
		exp, err := s.Store.Renew(a.session, a.agent, s.LeaseTTL)
		if err != nil {
			writeJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}
		s.append(w, a, "RENEW", map[string]any{"expires_ms": exp})
	}))
	mux.HandleFunc("POST /v1/say", s.signed("SAY", func(w http.ResponseWriter, r *http.Request, a authInfo, b []byte) {
		var in payload
		if err := decode(b, &in); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		s.append(w, a, "SAY", map[string]string{"body": in.Body})
	}))
	mux.HandleFunc("POST /v1/receipt", s.signed("RECEIPT", func(w http.ResponseWriter, r *http.Request, a authInfo, b []byte) {
		var in payload
		if err := decode(b, &in); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		if in.Commit == "" {
			writeJSON(w, 400, map[string]string{"error": "commit SHA is required"})
			return
		}
		s.append(w, a, "RECEIPT", map[string]any{"commit": in.Commit, "diff_sha": in.DiffSHA, "files": in.Files})
	}))
	mux.HandleFunc("GET /v1/leases", func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("session")
		ls, err := s.Store.Leases(session)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"leases": ls})
	})
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) {
		ev, err := s.Store.Events(r.URL.Query().Get("session"))
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, ev)
	})
	mux.HandleFunc("GET /v1/verify", func(w http.ResponseWriter, r *http.Request) {
		n, err := s.Store.Verify()
		if err != nil {
			writeJSON(w, 409, map[string]any{"ok": false, "checked": n, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "checked": n})
	})
	mux.HandleFunc("GET /v1/doc", func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("session")
		ev, err := s.Store.Events(session)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "text/asciidoc; charset=utf-8")
		_, _ = w.Write(render.AsciiDoc(session, ev))
	})
	return mux
}
func (s *Server) Listen(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("refusing non-loopback bind %q; remote access needs an authenticated TLS deployment", addr)
	}
	server := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	return server.ListenAndServe()
}
