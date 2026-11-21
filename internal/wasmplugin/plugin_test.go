package wasmplugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"collaborate/internal/adapter"
)

var (
	buildOnce sync.Once
	builtWASM string
	buildErr  error
)

func exampleWASM(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		_, sourceFile, _, _ := runtime.Caller(0)
		projectRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
		sourceDir := filepath.Join(projectRoot, "examples", "wasm", "openai")
		builtWASM = filepath.Join(os.TempDir(), "collaborate-openai-wasi-test.wasm")
		command := exec.Command("go", "build", "-trimpath", "-o", builtWASM, ".")
		command.Dir = sourceDir
		command.Env = make([]string, 0, len(os.Environ())+3)
		for _, value := range os.Environ() {
			if strings.HasPrefix(value, "GOOS=") || strings.HasPrefix(value, "GOARCH=") || strings.HasPrefix(value, "CGO_ENABLED=") {
				continue
			}
			command.Env = append(command.Env, value)
		}
		command.Env = append(command.Env, "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
		if output, err := command.CombinedOutput(); err != nil {
			buildErr = &buildFailure{err: err, output: string(output)}
		}
	})
	if buildErr != nil {
		t.Fatalf("compile sample WASI module: %v", buildErr)
	}
	return builtWASM
}

type buildFailure struct {
	err    error
	output string
}

func (e *buildFailure) Error() string { return e.err.Error() + ": " + e.output }

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

func TestWASIProviderRunsRequestAndResponsePhasesThroughHost(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("provider path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-secret" {
			t.Errorf("authorization = %q", got)
		}
		var request struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			Messages  []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != "sample-model" || request.MaxTokens != 256 || len(request.Messages) != 1 || request.Messages[0].Content != "prompt from agent" {
			t.Errorf("provider request = %#v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"WASI answer"}}]}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	module := filepath.Join(dir, "provider.wasm")
	data, err := os.ReadFile(exampleWASM(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(module, data, 0600); err != nil {
		t.Fatal(err)
	}
	moduleDigest := sha256.Sum256(data)
	manifest := Manifest{
		Name: "test-wasi-provider", Module: "provider.wasm", BaseURL: server.URL + "/v1",
		Model: "sample-model", APIKeyEnv: "WASI_TEST_KEY", AuthHeader: "Authorization",
		AuthPrefix: "Bearer ", MaxTokens: 256, SHA256: hex.EncodeToString(moduleDigest[:]),
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "plugin.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0600); err != nil {
		t.Fatal(err)
	}
	plugin, err := load(manifestPath, func(name string) (string, bool) {
		if name == "WASI_TEST_KEY" {
			return "test-secret", true
		}
		return "", false
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer plugin.Close(context.Background())

	broker := &testBroker{}
	got, err := plugin.Run(context.Background(), adapter.Request{Prompt: "prompt from agent"}, broker)
	if err != nil {
		t.Fatal(err)
	}
	if plugin.Name() != "test-wasi-provider" || got.Text != "WASI answer" || len(broker.messages) != 1 || broker.messages[0] != "WASI answer" {
		t.Fatalf("name=%q response=%#v broker=%#v", plugin.Name(), got, broker.messages)
	}
}

func TestValidateRequestPathRejectsOriginAndTraversal(t *testing.T) {
	for _, path := range []string{
		"", "relative", "//evil.example/path", "https://evil.example/path", "/../escape", "/%2e%2e/escape", "/v1/path?key=secret", "/v1/path#fragment", "/\\evil",
	} {
		t.Run(strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
			if err := validateRequestPath(path); err == nil {
				t.Fatalf("expected %q to be rejected", path)
			}
		})
	}
	for _, path := range []string{"/v1/messages", "/v1beta/models/gemini:generateContent", "/chat/completions"} {
		if err := validateRequestPath(path); err != nil {
			t.Errorf("valid path %q rejected: %v", path, err)
		}
	}
}

func TestManifestRejectsHeaderInjectionAndInvalidNames(t *testing.T) {
	base := Manifest{Name: "valid-provider", Module: "provider.wasm", BaseURL: "https://provider.example", Model: "model", APIKeyEnv: "PROVIDER_KEY", AuthHeader: "Authorization"}
	cases := []Manifest{
		{Name: "has space", Module: base.Module, BaseURL: base.BaseURL, Model: base.Model, APIKeyEnv: base.APIKeyEnv, AuthHeader: base.AuthHeader},
		{Name: base.Name, Module: base.Module, BaseURL: base.BaseURL, Model: base.Model, APIKeyEnv: base.APIKeyEnv, AuthHeader: "Host"},
		{Name: base.Name, Module: base.Module, BaseURL: base.BaseURL, Model: base.Model, APIKeyEnv: base.APIKeyEnv, AuthHeader: base.AuthHeader, AuthPrefix: "bad\r\nvalue"},
		{Name: base.Name, Module: base.Module, BaseURL: base.BaseURL, Model: base.Model, APIKeyEnv: base.APIKeyEnv, AuthHeader: base.AuthHeader, Headers: map[string]string{"Authorization": "Bearer injected"}},
		{Name: base.Name, Module: base.Module, BaseURL: base.BaseURL, Model: base.Model, APIKeyEnv: base.APIKeyEnv, AuthHeader: "Bad\nName"},
	}
	for i, manifest := range cases {
		if err := validateManifest(manifest); err == nil {
			t.Errorf("case %d should be rejected", i)
		}
	}
	insecure := base
	insecure.BaseURL = "http://127.0.0.1:8080"
	if err := validateManifest(insecure); err == nil {
		t.Fatal("dynamic plugin manifest must reject cleartext HTTP, including loopback")
	}
	for _, endpoint := range []string{"https://provider.example?", "https://provider.example#"} {
		ambiguous := base
		ambiguous.BaseURL = endpoint
		if err := validateManifest(ambiguous); err == nil {
			t.Errorf("dynamic plugin manifest accepted ambiguous base URL %q", endpoint)
		}
	}
}

func TestLoadRejectsModuleSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.wasm")
	if err := os.WriteFile(outside, []byte("not actually a module"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "provider.wasm")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	manifest := Manifest{Name: "escape-test", Module: "provider.wasm", BaseURL: "https://example.invalid", Model: "m", APIKeyEnv: "KEY", AuthHeader: "Authorization"}
	data, _ := json.Marshal(manifest)
	path := filepath.Join(dir, "plugin.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, func(string) (string, bool) { return "secret", true })
	if err == nil || !strings.Contains(err.Error(), "inside its manifest directory") {
		t.Fatalf("expected module escape rejection, got %v", err)
	}
}

func TestLoadRejectsModuleChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "provider.wasm"), []byte("changed module"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		Name: "checksum-test", Module: "provider.wasm", SHA256: strings.Repeat("0", 64),
		BaseURL: "https://example.invalid", Model: "model", APIKeyEnv: "PROVIDER_KEY", AuthHeader: "Authorization",
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "plugin.json")
	if err := os.WriteFile(manifestPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = Load(manifestPath, func(string) (string, bool) { return "secret", true })
	if err == nil || !strings.Contains(err.Error(), "SHA-256 does not match") {
		t.Fatalf("expected SHA-256 mismatch rejection, got %v", err)
	}
}
