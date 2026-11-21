package openai

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
	return adapter.EventAck{Seq: int64(len(b.messages))}, nil
}
func (b *testBroker) Release([]string) (adapter.EventAck, error) { return adapter.EventAck{}, nil }
func (b *testBroker) Submit(adapter.Receipt) (adapter.EventAck, error) {
	return adapter.EventAck{}, nil
}

func TestPluginCallsOpenAICompatibleEndpointAndPublishesViaBroker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Errorf("authorization header missing")
		}
		var payload struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Model != "fixture-model" || len(payload.Messages) != 1 || payload.Messages[0].Content != "prompt" {
			t.Errorf("request payload = %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"answer"}}]}`))
	}))
	defer server.Close()

	plugin, err := New(server.URL+"/v1", "fixture-model", "fixture-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	broker := &testBroker{}
	got, err := plugin.Run(context.Background(), adapter.Request{Prompt: "prompt"}, broker)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "answer" || !reflect.DeepEqual(broker.messages, []string{"answer"}) {
		t.Fatalf("response=%#v broker=%#v", got, broker.messages)
	}
	if plugin.Name() != "openai-compatible" {
		t.Fatalf("name = %q", plugin.Name())
	}
}
