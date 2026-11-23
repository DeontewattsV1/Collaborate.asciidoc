package agentterm

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"collaborate/internal/agentregistry"
)

//go:embed ui.html
var uiHTML []byte

// GitHubUser is the identity GitHub reports for a token.
type GitHubUser struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
	Type  string `json:"type"`
}

// Authenticator turns a bearer credential into a verified GitHub identity.
type Authenticator interface {
	User(ctx context.Context, token string) (GitHubUser, error)
}

// GitHubAuth asks the GitHub API who a token belongs to. The token is used
// for this one request and is never stored or logged.
type GitHubAuth struct {
	APIURL string
	Client *http.Client
}

// User implements Authenticator.
func (g GitHubAuth) User(ctx context.Context, token string) (GitHubUser, error) {
	api := strings.TrimRight(g.APIURL, "/")
	if api == "" {
		api = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/user", nil)
	if err != nil {
		return GitHubUser{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return GitHubUser{}, errors.New("could not reach the GitHub API")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return GitHubUser{}, fmt.Errorf("GitHub rejected the token (HTTP %d)", resp.StatusCode)
	}
	var u GitHubUser
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&u); err != nil || u.ID <= 0 || u.Login == "" {
		return GitHubUser{}, errors.New("unexpected GitHub /user response")
	}
	return u, nil
}

// Server is the HTTP face of a Terminal.
type Server struct {
	T         *Terminal
	Registry  *agentregistry.Registry
	Auth      Authenticator
	ReadToken string // optional; when set, reads require it
	Exec      ExecPolicy
	execSlots chan struct{}
	once      sync.Once
	ctx       context.Context
	cancel    context.CancelFunc
}

// ClockInRequest is posted with "Authorization: Bearer <GitHub token>".
type ClockInRequest struct {
	Instance string `json:"instance"`
	Branch   string `json:"branch"`
	PubKey   string `json:"pubkey"`
	Nonce    int64  `json:"nonce"` // unix milliseconds, within 5 minutes of server time
	Sig      string `json:"sig"`
}

// EntryRequest is any agent request after clock-in.
type EntryRequest struct {
	Session string `json:"session"`
	Kind    string `json:"kind"`
	Nonce   int64  `json:"nonce"`
	Body    string `json:"body"`
	Sig     string `json:"sig"`
}

func (s *Server) init() {
	s.once.Do(func() {
		s.execSlots = make(chan struct{}, 2)
		s.ctx, s.cancel = context.WithCancel(context.Background())
	})
}

// Shutdown stops background command execution.
func (s *Server) Shutdown() {
	s.init()
	s.cancel()
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	s.init()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.read(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'")
		_, _ = w.Write(uiHTML)
	}))
	mux.HandleFunc("GET /api/info", s.read(func(w http.ResponseWriter, r *http.Request) {
		snap := s.T.Snapshot()
		writeJSON(w, http.StatusOK, map[string]any{
			"protocol": "agentterm/v1", "server_key": hex.EncodeToString(s.T.PublicKey()),
			"head": snap.Head, "seq": snap.Seq, "ttl": snap.TTL, "exec": s.Exec.Enabled,
		})
	}))
	mux.HandleFunc("GET /api/state", s.read(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.T.Snapshot())
	}))
	mux.HandleFunc("GET /api/log", s.read(func(w http.ResponseWriter, r *http.Request) {
		since := parseSince(r.URL.Query().Get("since"), -1)
		entries, _ := s.T.Since(since, 1000)
		writeJSON(w, http.StatusOK, entries)
	}))
	mux.HandleFunc("GET /api/verify", s.read(func(w http.ResponseWriter, r *http.Request) {
		rep, err := s.T.Verify()
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "report": rep})
	}))
	mux.HandleFunc("GET /api/stream", s.read(s.stream))
	mux.HandleFunc("POST /api/clock-in", s.clockIn)
	mux.HandleFunc("POST /api/entries", s.entry)
	return mux
}

func (s *Server) read(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.ReadToken != "" {
			got := r.URL.Query().Get("token")
			if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
				got = strings.TrimPrefix(a, "Bearer ")
			}
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.ReadToken)) != 1 {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "read token required"})
				return
			}
		}
		h(w, r)
	}
}

// stream serves Server-Sent Events. A client that reconnects (EventSource
// does this automatically, sending Last-Event-ID) replays everything it
// missed, so leaving and rejoining never loses history.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	since := parseSince(r.URL.Query().Get("since"), -1)
	if id := r.Header.Get("Last-Event-ID"); id != "" {
		since = parseSince(id, since)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	for {
		entries, changed := s.T.Since(since, 500)
		for _, e := range entries {
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "id: %d\nevent: entry\ndata: %s\n\n", e.Seq, data)
			since = e.Seq
		}
		flusher.Flush()
		if len(entries) == 500 {
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
		}
	}
}

func (s *Server) clockIn(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || len(auth) < 12 {
		writeErr(w, http.StatusUnauthorized, errors.New("clock-in needs 'Authorization: Bearer <GitHub token>'"))
		return
	}
	var req ClockInRequest
	if err := decodeStrict(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	now := s.T.now()
	if d := now.Sub(time.UnixMilli(req.Nonce)); d > 5*time.Minute || d < -5*time.Minute {
		writeErr(w, http.StatusBadRequest, errors.New("clock-in nonce must be the current time in unix milliseconds"))
		return
	}
	pub, err := hex.DecodeString(req.PubKey)
	sig, err2 := hex.DecodeString(req.Sig)
	if err != nil || err2 != nil || len(pub) != ed25519.PublicKeySize ||
		!ed25519.Verify(pub, ClockInMessage(req.Instance, req.Branch, req.PubKey, req.Nonce), sig) {
		writeErr(w, http.StatusUnauthorized, errors.New("clock-in must be signed by the session key it registers"))
		return
	}
	user, err := s.Auth.User(r.Context(), strings.TrimPrefix(auth, "Bearer "))
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err)
		return
	}
	accountType := "user"
	if user.Type == "Bot" {
		accountType = "bot"
	}
	agent, err := s.Registry.ResolvePullRequestAgent(agentregistry.PullRequestIdentity{
		AccountType: accountType, Login: user.Login, GitHubUserID: user.ID}, req.Branch)
	if err != nil {
		writeErr(w, http.StatusForbidden, fmt.Errorf("not authorized: %w", err))
		return
	}
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	e, err := s.T.Append(Entry{
		Kind: KindClockIn, Session: "s-" + hex.EncodeToString(id), Agent: agent.ID, Nonce: req.Nonce,
		AgentSig: req.Sig, Body: MustBody(ClockInBody{Instance: req.Instance, Branch: req.Branch, PubKey: req.PubKey,
			GitHubLogin: user.Login, GitHubID: user.ID, AccountType: accountType}),
	})
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": e.Session, "agent": e.Agent, "entry": e})
}

func (s *Server) entry(w http.ResponseWriter, r *http.Request) {
	var req EntryRequest
	if err := decodeStrict(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !IsAgentKind(req.Kind) || req.Kind == KindClockIn {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("kind %q cannot be posted here", req.Kind))
		return
	}
	if err := CompactJSON(req.Body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sess, ok := s.T.session(req.Session)
	if !ok || !sess.Open {
		writeErr(w, http.StatusUnauthorized, errors.New("unknown or closed session; clock in again"))
		return
	}
	pub, _ := hex.DecodeString(sess.PubKey)
	sig, err := hex.DecodeString(req.Sig)
	if err != nil || !ed25519.Verify(pub, RequestMessage(req.Session, req.Kind, req.Nonce, req.Body), sig) {
		writeErr(w, http.StatusUnauthorized, errors.New("request is not signed by this session's key"))
		return
	}
	e, err := s.T.Append(Entry{Kind: req.Kind, Session: req.Session, Agent: sess.Agent, Nonce: req.Nonce, Body: req.Body, AgentSig: req.Sig})
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	if e.Kind == KindExecRequest {
		var b ExecRequest
		_ = json.Unmarshal([]byte(e.Body), &b)
		go s.runExec(e, b.Argv)
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) runExec(req Entry, argv []string) {
	select {
	case s.execSlots <- struct{}{}:
	case <-s.ctx.Done():
		return
	}
	defer func() { <-s.execSlots }()
	res := s.Exec.Run(s.ctx, argv)
	res.Request = req.Seq
	// If the session ended meanwhile, the pending request was dropped and
	// this append is rejected; the result is then simply not recorded.
	_, _ = s.T.Append(Entry{Kind: KindExecResult, Session: req.Session, Agent: req.Agent, Body: MustBody(res)})
}

func decodeStrict(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 256<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("request body: %w", err)
	}
	return nil
}

func parseSince(v string, def int64) int64 {
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < -1 {
		return def
	}
	return n
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
