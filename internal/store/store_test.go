package store

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
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
func TestLeaseRejectsTraversal(t *testing.T) {
	s, _, _ := testStore(t)
	if err := s.Claim("s1", "agent-a", "escape", []string{"../outside"}, time.Minute); err == nil {
		t.Fatal("accepted path traversal")
	}
}
