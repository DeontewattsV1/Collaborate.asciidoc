package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"collaborate/internal/adapter"
)

type testBroker struct{ messages []string }

func (b *testBroker) Claim(string, []string) (adapter.EventAck, error) {
	return adapter.EventAck{}, nil
}
func (b *testBroker) Write(string, []byte) (adapter.EventAck, error) { return adapter.EventAck{}, nil }
func (b *testBroker) Say(text string) (adapter.EventAck, error) {
	b.messages = append(b.messages, text)
	return adapter.EventAck{Seq: 1}, nil
}
func (b *testBroker) Release([]string) (adapter.EventAck, error) { return adapter.EventAck{}, nil }
func (b *testBroker) Submit(adapter.Receipt) (adapter.EventAck, error) {
	return adapter.EventAck{}, nil
}

func TestPluginCallsMessagesAPIAndPublishesViaBroker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "fixture-key" || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("Anthropic auth/version headers missing")
		}
		var payload struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			Messages  []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Model != "claude-fixture" || payload.MaxTokens <= 0 || len(payload.Messages) != 1 || payload.Messages[0].Content != "prompt" {
			t.Errorf("request payload = %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hello "},{"type":"text","text":"from Claude"}]}`))
	}))
	defer server.Close()

	plugin, err := New(server.URL, "claude-fixture", "fixture-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	broker := &testBroker{}
	got, err := plugin.Run(context.Background(), adapter.Request{Prompt: "prompt"}, broker)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "hello from Claude" || !reflect.DeepEqual(broker.messages, []string{"hello from Claude"}) {
		t.Fatalf("response=%#v broker=%#v", got, broker.messages)
	}
	if plugin.Name() != "anthropic" {
		t.Fatalf("name = %q", plugin.Name())
	}
}
