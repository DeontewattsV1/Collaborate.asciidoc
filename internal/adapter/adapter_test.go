package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type recordingBroker struct {
	says []string
	err  error
}

func (b *recordingBroker) Claim(string, []string) (EventAck, error) { return EventAck{}, nil }
func (b *recordingBroker) Write(string, []byte) (EventAck, error)   { return EventAck{}, nil }
func (b *recordingBroker) Say(body string) (EventAck, error) {
	b.says = append(b.says, body)
	return EventAck{Seq: int64(len(b.says)), Hash: "fixture-hash"}, b.err
}
func (b *recordingBroker) Release([]string) (EventAck, error) { return EventAck{}, nil }
func (b *recordingBroker) Submit(Receipt) (EventAck, error)   { return EventAck{}, nil }

func TestEchoIsDeterministicAndPublishesResponse(t *testing.T) {
	broker := &recordingBroker{}
	adapter := Echo{}
	for i := 0; i < 2; i++ {
		got, err := adapter.Run(context.Background(), Request{Prompt: "fixture input"}, broker)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if got.Text != "fixture input" {
			t.Fatalf("Run() text = %q, want prompt verbatim", got.Text)
		}
	}
	if !reflect.DeepEqual(broker.says, []string{"fixture input", "fixture input"}) {
		t.Fatalf("published messages = %#v", broker.says)
	}
	if adapter.Name() != "echo" {
		t.Fatalf("Name() = %q, want echo", adapter.Name())
	}
}

func TestEchoHonorsContextAndBrokerErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	broker := &recordingBroker{}
	if _, err := (Echo{}).Run(ctx, Request{Prompt: "ignored"}, broker); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() with canceled context error = %v, want context.Canceled", err)
	}
	if len(broker.says) != 0 {
		t.Fatalf("canceled run published messages: %#v", broker.says)
	}
	if _, err := (Echo{}).Run(context.Background(), Request{}, nil); err == nil {
		t.Fatal("Run() with nil broker succeeded")
	}
	wantErr := errors.New("broker unavailable")
	if _, err := (Echo{}).Run(context.Background(), Request{}, &recordingBroker{err: wantErr}); !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want broker error", err)
	}
}

func TestRegistryRegisterGetAndSortedNames(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(Echo{}); err != nil {
		t.Fatalf("Register(echo): %v", err)
	}
	if err := registry.Register(namedAdapter("fixture-z")); err != nil {
		t.Fatalf("Register(fixture-z): %v", err)
	}
	if err := registry.Register(namedAdapter("fixture-a")); err != nil {
		t.Fatalf("Register(fixture-a): %v", err)
	}
	gotNames := registry.Names()
	wantNames := []string{"echo", "fixture-a", "fixture-z"}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("Names() = %#v, want %#v", gotNames, wantNames)
	}
	got, err := registry.Get("echo")
	if err != nil || got.Name() != "echo" {
		t.Fatalf("Get(echo) = %v, %v", got, err)
	}
	if _, err := registry.Get("missing"); !errors.Is(err, ErrAdapterNotFound) {
		t.Fatalf("Get(missing) error = %v, want ErrAdapterNotFound", err)
	}
	if err := registry.Register(Echo{}); !errors.Is(err, ErrAdapterExists) {
		t.Fatalf("duplicate Register() error = %v, want ErrAdapterExists", err)
	}
}

type namedAdapter string

func (a namedAdapter) Name() string { return string(a) }
func (a namedAdapter) Run(context.Context, Request, Broker) (Response, error) {
	return Response{Text: string(a)}, nil
}

type fakeDoer struct {
	method string
	path   string
	body   any
	data   []byte
	err    error
}

func (d *fakeDoer) Do(method, path string, body any) ([]byte, error) {
	d.method, d.path, d.body = method, path, body
	return d.data, d.err
}

func TestSignedBrokerUsesExistingProtocolRoutes(t *testing.T) {
	cases := []struct {
		name       string
		invoke     func(*SignedBroker) (EventAck, error)
		wantPath   string
		wantFields map[string]any
	}{
		{
			name: "claim", wantPath: "/v1/claim",
			invoke:     func(b *SignedBroker) (EventAck, error) { return b.Claim("edit docs", []string{"README.md"}) },
			wantFields: map[string]any{"intent": "edit docs", "paths": []any{"README.md"}},
		},
		{
			name: "say", wantPath: "/v1/say",
			invoke:     func(b *SignedBroker) (EventAck, error) { return b.Say("hello") },
			wantFields: map[string]any{"body": "hello"},
		},
		{
			name: "write", wantPath: "/v1/write",
			invoke:     func(b *SignedBroker) (EventAck, error) { return b.Write("note.txt", []byte("x")) },
			wantFields: map[string]any{"path": "note.txt", "content": "eA=="},
		},
		{
			name: "release", wantPath: "/v1/release",
			invoke:     func(b *SignedBroker) (EventAck, error) { return b.Release([]string{"README.md"}) },
			wantFields: map[string]any{"paths": []any{"README.md"}},
		},
		{
			name: "submit", wantPath: "/v1/receipt",
			invoke: func(b *SignedBroker) (EventAck, error) {
				return b.Submit(Receipt{Commit: "abc123", DiffSHA: "def456", Files: []string{"README.md"}})
			},
			wantFields: map[string]any{"commit": "abc123", "diff_sha": "def456", "files": []any{"README.md"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doer := &fakeDoer{data: []byte(`{"seq":7,"hash":"ledger-hash"}`)}
			ack, err := tc.invoke(NewSignedBroker(doer))
			if err != nil {
				t.Fatalf("operation error = %v", err)
			}
			if doer.method != "POST" || doer.path != tc.wantPath {
				t.Fatalf("Do() = %s %s, want POST %s", doer.method, doer.path, tc.wantPath)
			}
			if ack != (EventAck{Seq: 7, Hash: "ledger-hash"}) {
				t.Fatalf("ack = %#v", ack)
			}
			encoded, err := json.Marshal(doer.body)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			if !reflect.DeepEqual(got, tc.wantFields) {
				t.Fatalf("body = %#v, want %#v", got, tc.wantFields)
			}
		})
	}
}

func TestSignedBrokerErrors(t *testing.T) {
	if _, err := NewSignedBroker(nil).Say("hello"); err == nil {
		t.Fatal("Say() with nil signed client succeeded")
	}
	if _, err := NewSignedBroker(&fakeDoer{data: []byte("not json")}).Say("hello"); err == nil {
		t.Fatal("Say() with malformed acknowledgement succeeded")
	}
	wantErr := errors.New("signed request failed")
	if _, err := NewSignedBroker(&fakeDoer{err: wantErr}).Say("hello"); !errors.Is(err, wantErr) {
		t.Fatalf("Say() error = %v, want underlying error", err)
	}
}

func TestOpenAICompatibleCallsProviderAndRecordsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("provider path=%q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Errorf("authorization header=%q", r.Header.Get("Authorization"))
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "fixture-model" || len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content != "prompt" {
			t.Errorf("provider payload=%+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"provider answer"}}]}`))
	}))
	defer server.Close()
	a, err := NewOpenAICompatible(server.URL+"/v1", "fixture-model", "test-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	broker := &recordingBroker{}
	got, err := a.Run(context.Background(), Request{Prompt: "prompt"}, broker)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "provider answer" || !reflect.DeepEqual(broker.says, []string{"provider answer"}) {
		t.Fatalf("response=%+v broker messages=%v", got, broker.says)
	}
}

func TestOpenAICompatibleRejectsCleartextRemoteEndpoint(t *testing.T) {
	if _, err := NewOpenAICompatible("http://api.example.com/v1", "fixture", "secret", nil); err == nil {
		t.Fatal("cleartext remote provider endpoint was accepted")
	}
	if _, err := NewOpenAICompatible("http://localhost:1234/v1", "fixture", "secret", nil); err != nil {
		t.Fatalf("loopback HTTP endpoint should be allowed for local testing: %v", err)
	}
}
