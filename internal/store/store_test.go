package store

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) (*Store, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	s, err := Open(t.TempDir() + "/ledger.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateSession("s1", "/tmp/repo"); err != nil {
		t.Fatal(err)
	}
	if err = s.Register("s1", "agent-a", "test-model", pub); err != nil {
		t.Fatal(err)
	}
	return s, pub, priv
}
func appendSigned(t *testing.T, s *Store, priv ed25519.PrivateKey, body string) Event {
	t.Helper()
	d := sha256.Sum256([]byte(body))
	sig := ed25519.Sign(priv, d[:])
	e, err := s.Append("s1", "agent-a", "SAY", map[string]string{"body": body}, fmtHex(d[:]), sig)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func fmtHex(b []byte) string { return fmt.Sprintf("%x", b) }
func TestVerifySignedChainAndDetectTamper(t *testing.T) {
	s, _, priv := testStore(t)
	appendSigned(t, s, priv, "hello")
	n, err := s.Verify()
	if err != nil || n != 1 {
		t.Fatalf("Verify()=(%d,%v), want (1,nil)", n, err)
	}
	if _, err = s.db.Exec(`UPDATE events SET payload='{"body":"changed"}' WHERE seq=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Verify(); err == nil {
		t.Fatal("Verify succeeded after ledger tampering")
	}
}
func TestLeaseContentionAndRelease(t *testing.T) {
	s, _, _ := testStore(t)
	if err := s.Claim("s1", "agent-a", "change file", []string{"src/a.go"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	err := s.Claim("s1", "agent-b", "competing change", []string{"src/a.go"}, time.Minute)
	if !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("second claim error=%v, want ErrLeaseHeld", err)
	}
	if err = s.Release("s1", "agent-a", []string{"src/a.go"}); err != nil {
		t.Fatal(err)
	}
	if err = s.Claim("s1", "agent-b", "after release", []string{"src/a.go"}, time.Minute); err != nil {
		t.Fatalf("claim after release: %v", err)
	}
}
func TestConcurrentSamePathClaimHasExactlyOneWinner(t *testing.T) {
	s, _, _ := testStore(t)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Register("s1", "agent-b", "test-model", pub); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, agent := range []string{"agent-a", "agent-b"} {
		wg.Add(1)
		go func(agent string) {
			defer wg.Done()
			<-start
			results <- s.Claim("s1", agent, "concurrent edit", []string{"src/shared.go"}, time.Minute)
		}(agent)
	}
	close(start)
	wg.Wait()
	close(results)

	var succeeded, held int
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrLeaseHeld):
			held++
		default:
			t.Fatalf("unexpected concurrent claim error: %v", err)
		}
	}
	if succeeded != 1 || held != 1 {
		t.Fatalf("claim outcomes: succeeded=%d held=%d, want exactly one of each", succeeded, held)
	}
}
func TestExpiredLeaseCanBeClaimedByDifferentAgent(t *testing.T) {
	s, _, _ := testStore(t)
	const path = "src/short-lived.go"
	ttl := 100 * time.Millisecond
	if err := s.Claim("s1", "agent-a", "temporary edit", []string{path}, ttl); err != nil {
		t.Fatal(err)
	}
	time.Sleep(ttl + 150*time.Millisecond)

	if err := s.Claim("s1", "agent-b", "take over expired lease", []string{path}, time.Minute); err != nil {
		t.Fatalf("different agent could not claim expired lease: %v", err)
	}
	leases, err := s.Leases("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 || leases[0]["agent"] != "agent-b" {
		t.Fatalf("leases after expired-lease takeover=%v, want one lease owned by agent-b", leases)
	}
}
func TestRestartPreservesEventsAndOnlyActiveLeases(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ledger.sqlite")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateSession("s1", "/tmp/repo"); err != nil {
		t.Fatal(err)
	}
	if err = s.Register("s1", "agent-a", "test-model", pub); err != nil {
		t.Fatal(err)
	}
	appendSigned(t, s, priv, "persisted event one")
	appendSigned(t, s, priv, "persisted event two")

	expiringTTL := 100 * time.Millisecond
	if err = s.Claim("s1", "agent-a", "active lease", []string{"src/active.go"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = s.Claim("s1", "agent-a", "expiring lease", []string{"src/expired.go"}, expiringTTL); err != nil {
		t.Fatal(err)
	}
	time.Sleep(expiringTTL + 150*time.Millisecond)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.Events("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Seq != 1 || events[1].Seq != 2 {
		t.Fatalf("events after reopen=%v, want two ordered events", events)
	}
	if n, err := s.Verify(); err != nil || n != 2 {
		t.Fatalf("Verify after reopen=(%d,%v), want (2,nil)", n, err)
	}
	leases, err := s.Leases("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 || leases[0]["path"] != "src/active.go" || leases[0]["agent"] != "agent-a" {
		t.Fatalf("leases after reopen=%v, want only active lease src/active.go owned by agent-a", leases)
	}
	if err = s.Claim("s1", "agent-b", "reclaim after restart", []string{"src/expired.go"}, time.Minute); err != nil {
		t.Fatalf("expired persisted lease blocked a new claim after reopen: %v", err)
	}
}
func TestLeaseRejectsTraversal(t *testing.T) {
	s, _, _ := testStore(t)
	if err := s.Claim("s1", "agent-a", "escape", []string{"../outside"}, time.Minute); err == nil {
		t.Fatal("accepted path traversal")
	}
}

func TestHierarchicalLeaseOverlapAndWriteCoverage(t *testing.T) {
	s, _, _ := testStore(t)
	if err := s.Claim("s1", "agent-a", "directory edit", []string{"src"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.Claim("s1", "agent-b", "nested file", []string{"src/a.go"}, time.Minute); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("nested claim error=%v, want ErrLeaseHeld", err)
	}
	if err := s.Claim("s1", "agent-b", "sibling directory", []string{"src2/a.go"}, time.Minute); err != nil {
		t.Fatalf("non-overlapping sibling claim: %v", err)
	}
	if ok, err := s.LeaseAllows("s1", "agent-a", "src/a.go"); err != nil || !ok {
		t.Fatalf("directory lease should cover descendant: (%v,%v)", ok, err)
	}
	if ok, err := s.LeaseAllows("s1", "agent-a", "src2/a.go"); err != nil || ok {
		t.Fatalf("directory lease must not cover sibling: (%v,%v)", ok, err)
	}
	if err := s.Release("s1", "agent-a", []string{"src"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Claim("s1", "agent-b", "narrow file lease", []string{"src/a.go"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.LeaseAllows("s1", "agent-b", "src"); err != nil || ok {
		t.Fatalf("narrow file lease must not authorize parent write: (%v,%v)", ok, err)
	}
}

func TestKeyRotationAndRevocationPreserveLedgerVerification(t *testing.T) {
	s, _, oldPriv := testStore(t)
	appendSigned(t, s, oldPriv, "before rotation")
	newPub, newPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	version, err := s.RotateKeyFrom("s1", "agent-a", 1, newPub)
	if err != nil || version != 2 {
		t.Fatalf("RotateKey=(%d,%v), want (2,nil)", version, err)
	}
	stalePub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RotateKeyFrom("s1", "agent-a", 1, stalePub); !errors.Is(err, ErrStaleKey) {
		t.Fatalf("stale RotateKeyFrom error=%v, want ErrStaleKey", err)
	}
	digest := sha256.Sum256([]byte("after rotation"))
	if _, err = s.AppendVersion("s1", "agent-a", "SAY", map[string]string{"body": "after rotation"}, fmtHex(digest[:]), ed25519.Sign(newPriv, digest[:]), version); err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeKey("s1", "agent-a", 1); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Verify(); err != nil || n != 2 {
		t.Fatalf("Verify after old-key revocation=(%d,%v), want (2,nil)", n, err)
	}
	a, err := s.Agent("s1", "agent-a")
	if err != nil || a.KeyVersion != 2 || a.KeyRevoked || !ed25519.PublicKey(a.Pubkey).Equal(newPub) {
		t.Fatalf("current agent key after rotation=%+v err=%v", a, err)
	}
	_, active, err := s.AgentKey("s1", "agent-a", 1)
	if err != nil || active {
		t.Fatalf("old key active=%v err=%v, want revoked", active, err)
	}
}

func TestReviewCannotCrossSessionBoundary(t *testing.T) {
	s, _, _ := testStore(t)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Register("s1", "agent-b", "test", pub); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateSession("s2", "/tmp/other-repo"); err != nil {
		t.Fatal(err)
	}
	if err = s.Register("s2", "agent-x", "test", pub); err != nil {
		t.Fatal(err)
	}
	queueID, err := s.Enqueue("s1", "agent-a", "deadbeef", "collab/s1/agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Review("s2", queueID, "agent-x", "approve", "cross-session attempt"); !errors.Is(err, ErrQueueSession) {
		t.Fatalf("cross-session Review error=%v, want ErrQueueSession", err)
	}
	if err = s.Review("s1", queueID, "agent-a", "approve", "self-review"); err == nil {
		t.Fatal("author was allowed to review own item")
	}
	if err = s.Review("s1", queueID, "agent-b", "approve", "same-session approval"); err != nil {
		t.Fatalf("same-session independent review: %v", err)
	}
}
