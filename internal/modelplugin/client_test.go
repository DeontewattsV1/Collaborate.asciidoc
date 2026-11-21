package modelplugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNewClientRejectsInvalidConfiguration(t *testing.T) {
	cases := []struct {
		name, endpoint, model, key string
	}{
		{name: "relative endpoint", endpoint: "/v1", model: "model", key: "secret"},
		{name: "cleartext non-loopback", endpoint: "http://example.invalid", model: "model", key: "secret"},
		{name: "URL credentials", endpoint: "https://user:pass@example.invalid", model: "model", key: "secret"},
		{name: "blank model", endpoint: "https://example.invalid", model: " ", key: "secret"},
		{name: "blank key", endpoint: "https://example.invalid", model: "model", key: " \t"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewClient(tc.endpoint, tc.model, tc.key, nil); err == nil {
				t.Fatal("expected invalid configuration to be rejected")
			}
		})
	}
}

func TestPostJSONDoesNotFollowRedirectOrForwardCredentials(t *testing.T) {
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Store(true)
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("credential forwarded to redirect target: %q", got)
		}
	}))
	defer target.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/capture", http.StatusFound)
	}))
	defer origin.Close()

	client, err := NewClient(origin.URL, "fixture", "secret", origin.Client())
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	err = client.PostJSON(context.Background(), "/inference", map[string]string{"Authorization": "Bearer secret"}, map[string]string{"prompt": "fixture"}, &response)
	if err == nil || !strings.Contains(err.Error(), "302 Found") {
		t.Fatalf("expected redirect response error, got %v", err)
	}
	if redirected.Load() {
		t.Fatal("provider client followed redirect")
	}
}
