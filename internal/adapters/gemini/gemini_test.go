package gemini

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

func TestPluginCallsGenerateContentAndPublishesViaBroker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-fixture:generateContent" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("x-goog-api-key") != "fixture-key" {
			t.Errorf("Gemini API key header missing")
		}
		if r.URL.Query().Has("key") {
			t.Error("API key must not be placed in the URL")
		}
		var payload struct {
			Contents []struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"contents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if len(payload.Contents) != 1 || len(payload.Contents[0].Parts) != 1 || payload.Contents[0].Parts[0].Text != "prompt" {
			t.Errorf("request payload = %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"Hello "},{"text":"Gemini"}]}}]}`))
	}))
	defer server.Close()

	plugin, err := New(server.URL, "gemini-fixture", "fixture-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	broker := &testBroker{}
	got, err := plugin.Run(context.Background(), adapter.Request{Prompt: "prompt"}, broker)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "Hello Gemini" || !reflect.DeepEqual(broker.messages, []string{"Hello Gemini"}) {
		t.Fatalf("response=%#v broker=%#v", got, broker.messages)
	}
	if plugin.Name() != "gemini" {
		t.Fatalf("name = %q", plugin.Name())
	}
}
