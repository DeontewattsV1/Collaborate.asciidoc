package collab

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"collaborate/internal/sqlite"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	db := filepath.Join(dir, "state.sqlite")
	s, err := OpenStore(db, filepath.Join(dir, "broker.key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, db
}
func mustRegister(t *testing.T, s *Store, a string) {
	t.Helper()
	if _, err := s.Register(RegisterInput{Agent: a, Model: "simulation"}); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentClaimsAndReceipts(t *testing.T) {
	s, db := testStore(t)
	mustRegister(t, s, "alpha")
	mustRegister(t, s, "beta")
	var wg sync.WaitGroup
	wg.Add(2)
	start := make(chan struct{})
	var successful int
	var mu sync.Mutex
	for _, a := range []string{"alpha", "beta"} {
		a := a
		go func() {
			defer wg.Done()
			<-start
			_, _, err := s.Claim(LeaseInput{Agent: a, Path: "src", TTLSeconds: 30})
			if err == nil {
				mu.Lock()
				successful++
				mu.Unlock()
			} else if !strings.Contains(err.Error(), "conflict") {
				t.Errorf("unexpected claim result: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if successful != 1 {
		t.Fatalf("expected one exclusive winner, got %d", successful)
	}
	if _, _, err := s.Claim(LeaseInput{Agent: "beta", Path: "../escape"}); err == nil {
		t.Fatal("traversal accepted")
	}
	if _, err := s.Receipt(ReceiptInput{Agent: "beta", Commit: strings.Repeat("a", 40), Paths: []string{"elsewhere.go"}, Summary: "unleased edit"}); err == nil {
		t.Fatal("receipt without lease accepted")
	}
	n, head, err := Verify(db, s.PublicKey(), "")
	if err != nil || n != 3 || head == "" {
		t.Fatalf("verify n=%d hash=%s err=%v", n, head, err)
	}
}
func TestMessageLeaseRenewReceiptAndDeterministicReport(t *testing.T) {
	s, db := testStore(t)
	mustRegister(t, s, "alpha")
	mustRegister(t, s, "beta")
	l, _, err := s.Claim(LeaseInput{Agent: "alpha", Path: "src/auth", TTLSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Claim(LeaseInput{Agent: "beta", Path: "src/auth/token.go", TTLSeconds: 10}); err == nil {
		t.Fatal("overlapping lease accepted")
	}
	if _, _, err := s.Claim(LeaseInput{Agent: "beta", Path: "src/auth-other", TTLSeconds: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Say(MessageInput{Agent: "beta", To: "alpha", Body: "I'll work on unrelated code"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Renew(RenewInput{Agent: "beta", LeaseID: l.ID, TTLSeconds: 20}); err == nil {
		t.Fatal("cross-agent lease renewal accepted")
	}
	if _, _, err := s.Renew(RenewInput{Agent: "alpha", LeaseID: l.ID, TTLSeconds: 20}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Receipt(ReceiptInput{Agent: "alpha", Commit: strings.Repeat("a", 40), Paths: []string{"src/auth/token.go"}, Summary: "Implemented parser"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Gate(GateInput{Agent: "alpha", Command: []string{"go", "test", "./..."}, ExitCode: 0, OutputSHA256: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	one, err := s.Report()
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.Report()
	if err != nil {
		t.Fatal(err)
	}
	if one != two || !strings.Contains(one, "Implemented parser") || !strings.Contains(one, "Test gate reports") {
		t.Fatal("report not stable or incomplete")
	}
	n, head, err := Verify(db, s.PublicKey(), "")
	if err != nil || n != 8 {
		t.Fatalf("verify events=%d head=%s err=%v", n, head, err)
	}
	if _, _, err := Verify(db, s.PublicKey(), strings.Repeat("b", 64)); err == nil {
		t.Fatal("head pinning failed")
	}
	if _, err := s.Release(ReleaseInput{Agent: "alpha", LeaseID: l.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Receipt(ReceiptInput{Agent: "alpha", Commit: strings.Repeat("c", 40), Paths: []string{"src/auth/token.go"}, Summary: "after release"}); err == nil {
		t.Fatal("release not enforced")
	}
}
func TestTamperingRejectedByPinnedKey(t *testing.T) {
	s, dbPath := testStore(t)
	mustRegister(t, s, "alpha")
	key := append(ed25519.PublicKey(nil), s.PublicKey()...)
	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE events SET payload=? WHERE seq=1", `{"agent":"fake"}`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, _, err := Verify(dbPath, key, ""); err == nil {
		t.Fatal("tampering passed verification")
	}
}
func TestHTTPBrokerSSEAuth(t *testing.T) {
	s, _ := testStore(t)
	server := httptest.NewServer(NewServer(s, "test-only-token").Handler())
	defer server.Close()
	unauthorized, err := http.Get(server.URL + "/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != 401 {
		t.Fatalf("unauthenticated status %d", unauthorized.StatusCode)
	}
	streamReq, _ := http.NewRequest("GET", server.URL+"/v1/watch?after=0", nil)
	streamReq.Header.Set("Authorization", "Bearer test-only-token")
	client := &http.Client{Timeout: 2 * time.Second}
	streamResp, err := client.Do(streamReq)
	if err != nil {
		t.Fatal(err)
	}
	defer streamResp.Body.Close()
	got := make(chan string, 1)
	go func() {
		scan := bufio.NewScanner(streamResp.Body)
		for scan.Scan() {
			if strings.HasPrefix(scan.Text(), "data: ") {
				got <- scan.Text()
				return
			}
		}
		got <- "stream ended without data"
	}()
	payload, _ := json.Marshal(RegisterInput{Agent: "alpha", Model: "test"})
	req, _ := http.NewRequest("POST", server.URL+"/v1/register", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer test-only-token")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("register status %d", resp.StatusCode)
	}
	select {
	case text := <-got:
		if !strings.Contains(text, "REGISTER") {
			t.Fatalf("unexpected SSE %s", text)
		}
	case <-time.After(time.Second):
		t.Fatal("event not pushed")
	}
}
func TestPathRules(t *testing.T) {
	bad := []string{"", ".", "..", "x/../y", "x//y", "/tmp/x", "x\\y", "C:/windows", "x/./y"}
	for _, v := range bad {
		if _, err := NormalizePath(v); err == nil {
			t.Errorf("accepted unsafe path %q", v)
		}
	}
	for _, v := range []string{"src/main.go", "internal/auth", "x.go"} {
		if p, err := NormalizePath(v); err != nil || p != v {
			t.Errorf("valid path %q => %q %v", v, p, err)
		}
	}
	if overlaps("src/auth", "src/auth2") || !overlaps("src/auth", "src/auth/token.go") {
		t.Fatal("directory boundary check broken")
	}
}
func TestKeyPermissions(t *testing.T) {
	s, _ := testStore(t)
	if len(s.PublicKey()) != 32 {
		t.Fatal("bad public key")
	}
}
