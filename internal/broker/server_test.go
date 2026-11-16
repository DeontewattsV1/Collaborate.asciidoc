package broker

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"collaborate/internal/repoops"
	"collaborate/internal/store"
)

func signedTestRequest(t *testing.T, url, method, path, session, agent string, body []byte, priv ed25519.PrivateKey) *http.Request {
	t.Helper()
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		t.Fatal(err)
	}
	nonce := hex.EncodeToString(nonceBytes)

	// Match the request digest and Ed25519 signing layout in internal/client.Client.Do.
	h := sha256.New()
	for _, part := range [][]byte{[]byte(method), {0}, []byte(path), {0}, []byte(ts), {0}, []byte(nonce), {0}, body} {
		_, _ = h.Write(part)
	}
	sig := ed25519.Sign(priv, h.Sum(nil))
	req, err := http.NewRequest(method, url+path, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Collab-Agent", agent)
	req.Header.Set("X-Collab-Session", session)
	req.Header.Set("X-Collab-Timestamp", ts)
	req.Header.Set("X-Collab-Nonce", nonce)
	req.Header.Set("X-Collab-Signature", hex.EncodeToString(sig))
	return req
}

func TestSignedRequestReplayIsRejected(t *testing.T) {
	s, err := store.Open(t.TempDir() + "/broker.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateSession("session-1", "/tmp/repo"); err != nil {
		t.Fatal(err)
	}
	if err = s.Register("session-1", "agent-a", "test-model", pub); err != nil {
		t.Fatal(err)
	}

	httpServer := httptest.NewServer((&Server{Store: s, LeaseTTL: time.Minute}).Handler())
	defer httpServer.Close()

	body := []byte(`{"body":"hello"}`)
	firstRequest := signedTestRequest(t, httpServer.URL, http.MethodPost, "/v1/say", "session-1", "agent-a", body, priv)
	client := httpServer.Client()
	firstResponse, err := client.Do(firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, firstResponse.Body)
	firstResponse.Body.Close()
	if firstResponse.StatusCode != http.StatusOK {
		t.Fatalf("first request status=%d, want %d", firstResponse.StatusCode, http.StatusOK)
	}

	// Replay the exact signed method, URI, body, timestamp, nonce, and signature.
	replayRequest, err := http.NewRequest(http.MethodPost, httpServer.URL+"/v1/say", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	replayRequest.Header = firstRequest.Header.Clone()
	replayResponse, err := client.Do(replayRequest)
	if err != nil {
		t.Fatal(err)
	}
	replayBody, err := io.ReadAll(replayResponse.Body)
	replayResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if replayResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replay status=%d body=%s, want %d", replayResponse.StatusCode, replayBody, http.StatusUnauthorized)
	}
	if !strings.Contains(string(replayBody), "replayed request nonce") {
		t.Fatalf("replay response=%s, want replayed-nonce error", replayBody)
	}

	events, err := s.Events("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "SAY" {
		t.Fatalf("events after replay=%v, want exactly the first SAY event", events)
	}
}

func doSignedJSON(t *testing.T, hc *http.Client, base, method, path, session, agent string, body any, priv ed25519.PrivateKey) (int, []byte) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := signedTestRequest(t, base, method, path, session, agent, b, priv)
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, out
}
func gitBrokerTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func TestLeaseWriteReceiptReviewAndIntegrationWorkflow(t *testing.T) {
	root := t.TempDir()
	gitBrokerTest(t, root, "init", "-b", "main")
	gitBrokerTest(t, root, "config", "user.name", "Broker Test")
	gitBrokerTest(t, root, "config", "user.email", "broker-test@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("base\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitBrokerTest(t, root, "add", "note.txt")
	gitBrokerTest(t, root, "commit", "-m", "base")

	s, err := store.Open(filepath.Join(t.TempDir(), "workflow.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pubA, privA, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB, privB, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trunk := "collab/session-1/trunk"
	if err = repoops.EnsureBranch(root, trunk); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateSessionWithTrunk("session-1", root, trunk); err != nil {
		t.Fatal(err)
	}
	if err = s.Register("session-1", "agent-a", "test", pubA); err != nil {
		t.Fatal(err)
	}
	if err = s.Register("session-1", "agent-b", "test", pubB); err != nil {
		t.Fatal(err)
	}
	gates := map[string][]repoops.Gate{"session-1": {{Name: "content-check", Command: []string{"sh", "-c", "grep -q authorized note.txt"}, TimeoutSeconds: 10}}}
	handler := (&Server{Store: s, LeaseTTL: time.Minute, Gates: gates}).Handler()
	ts := httptest.NewServer(handler)
	defer ts.Close()
	hc := ts.Client()

	branch := "collab/session-1/agent-a"
	status, body := doSignedJSON(t, hc, ts.URL, http.MethodPost, "/v1/worktree", "session-1", "agent-a", map[string]string{"branch": branch}, privA)
	if status != http.StatusCreated {
		t.Fatalf("worktree status=%d body=%s", status, body)
	}
	var wt struct {
		Path string `json:"path"`
	}
	if err = json.Unmarshal(body, &wt); err != nil || wt.Path == "" {
		t.Fatalf("worktree response=%s err=%v", body, err)
	}

	status, body = doSignedJSON(t, hc, ts.URL, http.MethodPost, "/v1/write", "session-1", "agent-a", map[string]any{"path": "note.txt", "content": []byte("unauthorized\n")}, privA)
	if status != http.StatusConflict {
		t.Fatalf("unleased write status=%d body=%s, want 409", status, body)
	}
	status, body = doSignedJSON(t, hc, ts.URL, http.MethodPost, "/v1/claim", "session-1", "agent-a", map[string]any{"intent": "edit note", "paths": []string{"note.txt"}}, privA)
	if status != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", status, body)
	}
	status, body = doSignedJSON(t, hc, ts.URL, http.MethodPost, "/v1/write", "session-1", "agent-a", map[string]any{"path": "note.txt", "content": []byte("authorized\n")}, privA)
	if status != http.StatusOK {
		t.Fatalf("leased write status=%d body=%s", status, body)
	}
	gitBrokerTest(t, wt.Path, "add", "note.txt")
	gitBrokerTest(t, wt.Path, "commit", "-m", "authorized update")
	commit := gitBrokerTest(t, wt.Path, "rev-parse", "HEAD")

	status, body = doSignedJSON(t, hc, ts.URL, http.MethodPost, "/v1/receipt", "session-1", "agent-a", map[string]any{"commit": commit, "diff_sha": "client-lie", "files": []string{"wrong.txt"}}, privA)
	if status != http.StatusOK {
		t.Fatalf("receipt status=%d body=%s", status, body)
	}
	var receiptResponse struct {
		Receipt struct {
			DiffSHA        string   `json:"diff_sha"`
			Files          []string `json:"files"`
			ServerVerified bool     `json:"server_verified"`
			GateStatus     string   `json:"gate_status"`
		} `json:"receipt"`
	}
	if err = json.Unmarshal(body, &receiptResponse); err != nil {
		t.Fatal(err)
	}
	rc := receiptResponse.Receipt
	if !rc.ServerVerified || rc.DiffSHA == "client-lie" || len(rc.Files) != 1 || rc.Files[0] != "note.txt" || rc.GateStatus != "passed" {
		t.Fatalf("server receipt not independently derived: %+v", rc)
	}

	status, body = doSignedJSON(t, hc, ts.URL, http.MethodPost, "/v1/submit", "session-1", "agent-a", map[string]string{"commit": commit}, privA)
	if status != http.StatusCreated {
		t.Fatalf("submit status=%d body=%s", status, body)
	}
	var queued struct {
		QueueID int64 `json:"queue_id"`
	}
	if err = json.Unmarshal(body, &queued); err != nil || queued.QueueID < 1 {
		t.Fatalf("queue response=%s err=%v", body, err)
	}
	status, body = doSignedJSON(t, hc, ts.URL, http.MethodPost, "/v1/review", "session-1", "agent-b", map[string]any{"queue_id": queued.QueueID, "verdict": "approve", "reason": "looks good"}, privB)
	if status != http.StatusOK {
		t.Fatalf("review status=%d body=%s", status, body)
	}
	status, body = doSignedJSON(t, hc, ts.URL, http.MethodPost, "/v1/integrate", "session-1", "agent-a", map[string]int64{"queue_id": queued.QueueID}, privA)
	if status != http.StatusOK {
		t.Fatalf("integrate status=%d body=%s", status, body)
	}
	item, err := s.QueueItem(queued.QueueID)
	if err != nil || item.State != "merged" {
		t.Fatalf("queue item after integration=%+v err=%v", item, err)
	}
	merged := gitBrokerTest(t, root, "show", trunk+":note.txt")
	if merged != "authorized" {
		t.Fatalf("integration trunk content=%q", merged)
	}
	if n, err := s.Verify(); err != nil || n < 6 {
		t.Fatalf("final ledger verification=(%d,%v)", n, err)
	}
}

func TestOperatorTokenProtectsBootstrapAndRemoteBind(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "admin.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	srv := &Server{Store: s, AdminToken: "operator-secret"}
	route := srv.Handler()
	request := func(token string) int {
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{"id":"bootstrap-1"}`))
		if token != "" {
			req.Header.Set("X-Collab-Admin-Token", token)
		}
		w := httptest.NewRecorder()
		route.ServeHTTP(w, req)
		return w.Code
	}
	if code := request(""); code != http.StatusUnauthorized {
		t.Fatalf("bootstrap without token status=%d, want 401", code)
	}
	if code := request("wrong"); code != http.StatusUnauthorized {
		t.Fatalf("bootstrap with wrong token status=%d, want 401", code)
	}
	if code := request("operator-secret"); code != http.StatusCreated {
		t.Fatalf("bootstrap with token status=%d, want 201", code)
	}
	if err := srv.ListenTLS("0.0.0.0:0", "", "", ""); err == nil || !strings.Contains(err.Error(), "requires server TLS") {
		t.Fatalf("remote bind guard error=%v", err)
	}
}

func TestExportRecoveryAppendsMissingSuffixAndRejectsTampering(t *testing.T) {
	db := filepath.Join(t.TempDir(), "export.sqlite")
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateSession("s1", "/tmp/repo"); err != nil {
		t.Fatal(err)
	}
	if err = s.Register("s1", "agent-a", "test", pub); err != nil {
		t.Fatal(err)
	}
	appendEvent := func(body string) {
		t.Helper()
		digest := sha256.Sum256([]byte(body))
		if _, e := s.Append("s1", "agent-a", "SAY", map[string]string{"body": body}, hex.EncodeToString(digest[:]), ed25519.Sign(priv, digest[:])); e != nil {
			t.Fatal(e)
		}
	}
	appendEvent("first")
	exportDir := filepath.Join(t.TempDir(), "backup")
	srv := &Server{Store: s, ExportDir: exportDir}
	if err = srv.syncExport(); err != nil {
		t.Fatal(err)
	}
	appendEvent("second")
	if err = srv.syncExport(); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(exportDir, "events.jsonl")
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("backup lines=%d, want 2", len(lines))
	}
	if err = os.WriteFile(name, []byte(strings.Replace(string(raw), `"first"`, `"tampered"`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err = srv.syncExport(); err == nil {
		t.Fatal("tampered export prefix was accepted")
	}
}

func TestWriteShimRejectsTraversalAndSymlinkParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileUnder(root, "../outside.txt", []byte("x")); err == nil {
		t.Fatal("path traversal was accepted")
	}
	if err := writeFileUnder(root, "escape/newdir/file.txt", []byte("x")); err == nil {
		t.Fatal("symlinked parent was accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "newdir")); !os.IsNotExist(err) {
		t.Fatalf("symlink escape created an outside directory: err=%v", err)
	}
	if err := writeFileUnder(root, "safe/nested/file.txt", []byte("safe")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "safe", "nested", "file.txt"))
	if err != nil || string(got) != "safe" {
		t.Fatalf("safe write got=%q err=%v", got, err)
	}
}

func TestSessionReadRoutesAndFullExportRequireAuthorization(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "scope.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pubA, privA, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateSession("session-a", "/tmp/a"); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateSession("session-b", "/tmp/b"); err != nil {
		t.Fatal(err)
	}
	if err = s.Register("session-a", "agent-a", "test", pubA); err != nil {
		t.Fatal(err)
	}
	if err = s.Register("session-b", "agent-b", "test", pubB); err != nil {
		t.Fatal(err)
	}
	srv := &Server{Store: s, AdminToken: "admin-only"}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	for _, route := range []string{"/v1/events?session=session-b", "/v1/queue?session=session-b", "/v1/leases?session=session-b", "/v1/doc?session=session-b"} {
		req := signedTestRequest(t, ts.URL, http.MethodGet, route, "session-a", "agent-a", []byte("{}"), privA)
		resp, e := ts.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("cross-session read %s status=%d, want 403", route, resp.StatusCode)
		}
	}
	resp, err := ts.Client().Get(ts.URL + "/v1/export")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("export without operator token status=%d, want 401", resp.StatusCode)
	}
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/export", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Collab-Admin-Token", "admin-only")
	resp, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorized export status=%d, want 200", resp.StatusCode)
	}
}
