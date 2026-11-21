package broker

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"collaborate/internal/store"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newTestCA(t *testing.T, name string) testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return testCA{cert: cert, key: key, pool: pool}
}

func (ca testCA) issue(t *testing.T, cn string, usage x509.ExtKeyUsage) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// TestHealthProbeBypassesMTLSOnly checks, over real TLS handshakes, that a
// client without a certificate can reach GET/HEAD /health and nothing else,
// that a trusted client certificate still reaches the API, and that an
// untrusted certificate is still rejected at the handshake.
func TestHealthProbeBypassesMTLSOnly(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "health.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	srv := &Server{Store: s, AdminToken: "operator-secret"}

	ca := newTestCA(t, "test-ca")
	serverCert := ca.issue(t, "broker", x509.ExtKeyUsageServerAuth)
	cfg, handler := tlsServing(srv.Handler(), serverCert, ca.pool)
	if cfg.ClientAuth != tls.VerifyClientCertIfGiven {
		t.Fatalf("ClientAuth=%v, want VerifyClientCertIfGiven", cfg.ClientAuth)
	}
	ts := httptest.NewUnstartedServer(handler)
	ts.TLS = cfg
	ts.StartTLS()
	defer ts.Close()

	client := func(certs ...tls.Certificate) *http.Client {
		return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: ca.pool, Certificates: certs}}}
	}
	do := func(c *http.Client, method, path, token string) (int, error) {
		var body *strings.Reader
		if method == http.MethodPost {
			body = strings.NewReader(`{"id":"probe-session"}`)
		} else {
			body = strings.NewReader("")
		}
		req, err := http.NewRequest(method, ts.URL+path, body)
		if err != nil {
			return 0, err
		}
		if token != "" {
			req.Header.Set("X-Collab-Admin-Token", token)
		}
		resp, err := c.Do(req)
		if err != nil {
			return 0, err
		}
		resp.Body.Close()
		return resp.StatusCode, nil
	}

	anon := client()
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/health", http.StatusOK},
		{http.MethodHead, "/health", http.StatusOK},
		{http.MethodPost, "/health", http.StatusUnauthorized},
		{http.MethodGet, "/health/", http.StatusUnauthorized},
		{http.MethodGet, "/health?x=1", http.StatusUnauthorized},
		{http.MethodGet, "/v1/leases", http.StatusUnauthorized},
		{http.MethodPost, "/v1/sessions", http.StatusUnauthorized},
	} {
		token := ""
		if tc.path == "/v1/sessions" {
			token = "operator-secret" // operator token alone must not bypass mTLS
		}
		code, err := do(anon, tc.method, tc.path, token)
		if err != nil {
			t.Fatalf("anonymous %s %s: %v", tc.method, tc.path, err)
		}
		if code != tc.want {
			t.Fatalf("anonymous %s %s status=%d, want %d", tc.method, tc.path, code, tc.want)
		}
	}

	trusted := client(ca.issue(t, "agent", x509.ExtKeyUsageClientAuth))
	if code, err := do(trusted, http.MethodPost, "/v1/sessions", "operator-secret"); err != nil || code != http.StatusCreated {
		t.Fatalf("trusted client create session status=%d err=%v, want 201", code, err)
	}
	if code, err := do(trusted, http.MethodGet, "/health", ""); err != nil || code != http.StatusOK {
		t.Fatalf("trusted client health status=%d err=%v, want 200", code, err)
	}

	// Force the rogue certificate onto the wire: Go's client otherwise omits a
	// certificate that does not match the server's acceptable CAs.
	rogue := newTestCA(t, "rogue-ca")
	rogueCert := rogue.issue(t, "intruder", x509.ExtKeyUsageClientAuth)
	untrusted := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: ca.pool,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &rogueCert, nil
		},
	}}}
	for _, path := range []string{"/health", "/v1/leases"} {
		if _, err := do(untrusted, http.MethodGet, path, ""); err == nil {
			t.Fatalf("untrusted client certificate accepted for %s; want handshake failure", path)
		}
	}
}

func TestTLSServingWithoutClientCAIsPlainTLS(t *testing.T) {
	ca := newTestCA(t, "test-ca")
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	cfg, got := tlsServing(h, ca.issue(t, "broker", x509.ExtKeyUsageServerAuth), nil)
	if cfg.ClientAuth != tls.NoClientCert || cfg.ClientCAs != nil {
		t.Fatalf("unexpected client auth config: %v", cfg.ClientAuth)
	}
	if got == nil {
		t.Fatal("handler is nil")
	}
}
