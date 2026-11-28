package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func tagged(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", name)
		w.Header().Set("X-Path", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	})
}

func TestRouting(t *testing.T) {
	h := newHandler(tagged("web"), tagged("term"))
	cases := []struct{ path, upstream, upstreamPath string }{
		{"/", "web", "/"},
		{"/api/health", "web", "/api/health"},
		{"/api/guild/secrets", "web", "/api/guild/secrets"},
		{"/terminal", "term", "/"},
		{"/terminal/", "term", "/"},
		{"/terminal/api/info", "term", "/api/info"},
		{"/api/state", "term", "/api/state"},
		{"/api/stream", "term", "/api/stream"},
		{"/terminalx", "web", "/terminalx"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if got := rec.Header().Get("X-Upstream"); got != c.upstream {
			t.Errorf("%s: upstream %q, want %q", c.path, got, c.upstream)
		}
		if got := rec.Header().Get("X-Path"); got != c.upstreamPath {
			t.Errorf("%s: upstream path %q, want %q", c.path, got, c.upstreamPath)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := newHandler(tagged("web"), tagged("term"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	for k := range securityHeaders {
		if rec.Header().Get(k) == "" {
			t.Errorf("missing %s", k)
		}
	}
}

func TestPlainHTTPRedirects(t *testing.T) {
	h := newHandler(tagged("web"), tagged("term"))
	req := httptest.NewRequest(http.MethodGet, "http://collaborate-asciidoc.fly.dev/rooms?x=1", nil)
	req.Header.Set("X-Forwarded-Proto", "http")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPermanentRedirect {
		t.Fatalf("status %d, want 308", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://collaborate-asciidoc.fly.dev/rooms?x=1" {
		t.Fatalf("location %q", loc)
	}
	if rec.Header().Get("X-Upstream") != "" {
		t.Fatal("redirect must not reach an upstream")
	}
}

func TestHealthCheckWithoutForwardedProtoPasses(t *testing.T) {
	h := newHandler(tagged("web"), tagged("term"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("X-Upstream") != "web" {
		t.Fatalf("health check: %d %q", rec.Code, rec.Header().Get("X-Upstream"))
	}
}

func TestHealthIsNeverRedirected(t *testing.T) {
	h := newHandler(tagged("web"), tagged("term"))
	req := httptest.NewRequest(http.MethodGet, "http://collaborate-asciidoc.fly.dev/api/health", nil)
	req.Header.Set("X-Forwarded-Proto", "http")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health redirected: %d", rec.Code)
	}
}
