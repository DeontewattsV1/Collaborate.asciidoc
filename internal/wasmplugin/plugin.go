// Package wasmplugin loads third-party provider adapters as WASI WebAssembly modules.
// Guest modules have no network, filesystem, environment, or broker access. They
// build a provider request and parse the response; the host performs one bounded
// HTTP request to the manifest's fixed origin and publishes text via the broker.
package wasmplugin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"collaborate/internal/adapter"
	"collaborate/internal/modelplugin"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

const (
	maxManifestBytes = 1 << 20
	maxModuleBytes   = 32 << 20
	maxGuestIOBytes  = 8 << 20
	maxRequestBytes  = 2 << 20
	maxTextBytes     = 1 << 20
	guestMemoryPages = 512 // 32 MiB per WebAssembly memory
	defaultMaxTokens = 1024
	guestTimeout     = 15 * time.Second
)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var pluginNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// Manifest describes a WASI provider module and the narrow host capabilities
// it may use. The API key itself is never stored in the manifest or passed to
// guest code; only the host reads the named environment variable.
type Manifest struct {
	Name       string            `json:"name"`
	Module     string            `json:"module"`
	SHA256     string            `json:"sha256,omitempty"`
	BaseURL    string            `json:"base_url"`
	Model      string            `json:"model"`
	APIKeyEnv  string            `json:"api_key_env"`
	AuthHeader string            `json:"auth_header"`
	AuthPrefix string            `json:"auth_prefix,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	MaxTokens  int               `json:"max_tokens,omitempty"`
}

// Adapter is a third-party provider implementation running in a constrained
// WASI sandbox. A loaded instance can be used concurrently; each guest call is
// a fresh stateless process invocation.
type Adapter struct {
	manifest Manifest
	runtime  wazero.Runtime
	module   wazero.CompiledModule
	client   modelplugin.Client
}

// Load reads a strict JSON manifest and WASM module. lookupEnv is injectable for
// callers/tests; pass os.LookupEnv in normal CLI usage. The module must resolve
// inside the manifest directory, including after symlinks are resolved.
func Load(manifestPath string, lookupEnv func(string) (string, bool)) (*Adapter, error) {
	return load(manifestPath, lookupEnv, nil)
}

func load(manifestPath string, lookupEnv func(string) (string, bool), httpClient *http.Client) (*Adapter, error) {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	manifestAbs, err := filepath.Abs(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("resolve plugin manifest: %w", err)
	}
	manifest, err := ReadManifest(manifestAbs)
	if err != nil {
		return nil, err
	}
	key, ok := lookupEnv(manifest.APIKeyEnv)
	if !ok || strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("provider API key environment variable %s is not set", manifest.APIKeyEnv)
	}
	key = strings.TrimSpace(key)
	client, err := modelplugin.NewClient(manifest.BaseURL, manifest.Model, key, httpClient)
	if err != nil {
		return nil, fmt.Errorf("invalid plugin provider configuration: %w", err)
	}

	manifestDir, err := filepath.EvalSymlinks(filepath.Dir(manifestAbs))
	if err != nil {
		return nil, fmt.Errorf("resolve plugin directory: %w", err)
	}
	if filepath.IsAbs(manifest.Module) {
		return nil, errors.New("plugin module path must be relative to its manifest")
	}
	moduleCandidate := filepath.Join(manifestDir, filepath.Clean(manifest.Module))
	modulePath, err := filepath.EvalSymlinks(moduleCandidate)
	if err != nil {
		return nil, fmt.Errorf("resolve plugin module: %w", err)
	}
	rel, err := filepath.Rel(manifestDir, modulePath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, errors.New("plugin module must remain inside its manifest directory")
	}
	moduleFile, err := os.Open(modulePath)
	if err != nil {
		return nil, fmt.Errorf("open plugin module: %w", err)
	}
	defer moduleFile.Close()
	moduleBytes, err := io.ReadAll(io.LimitReader(moduleFile, maxModuleBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read plugin module: %w", err)
	}
	if len(moduleBytes) > maxModuleBytes {
		return nil, errors.New("WASM plugin module exceeds 32 MiB")
	}
	if manifest.SHA256 != "" {
		actual := sha256.Sum256(moduleBytes)
		if !strings.EqualFold(manifest.SHA256, hex.EncodeToString(actual[:])) {
			return nil, errors.New("WASM plugin SHA-256 does not match the manifest pin")
		}
	}

	ctx := context.Background()
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithMemoryLimitPages(guestMemoryPages).
		WithCloseOnContextDone(true).
		WithDebugInfoEnabled(false))
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		runtime.Close(ctx)
		return nil, fmt.Errorf("initialize WASI runtime: %w", err)
	}
	compiled, err := runtime.CompileModule(ctx, moduleBytes)
	if err != nil {
		runtime.Close(ctx)
		return nil, fmt.Errorf("compile WASM provider module: %w", err)
	}
	return &Adapter{manifest: manifest, runtime: runtime, module: compiled, client: client}, nil
}

// ReadManifest strictly reads provider metadata but does not resolve the module,
// inspect credentials, compile, or execute code. It is safe for CLI discovery.
func ReadManifest(path string) (Manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("open plugin manifest: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	if err != nil {
		return Manifest{}, fmt.Errorf("read plugin manifest: %w", err)
	}
	if len(data) > maxManifestBytes {
		return Manifest{}, errors.New("plugin manifest exceeds 1 MiB")
	}
	var manifest Manifest
	if err := decodeStrict(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode plugin manifest: %w", err)
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Discover reads all JSON manifests in a directory in deterministic filename
// order. It does not load modules or require their API key environment values.
func Discover(directory string) ([]Manifest, error) {
	entries, err := filepath.Glob(filepath.Join(directory, "*.json"))
	if err != nil {
		return nil, err
	}
	manifests := make([]Manifest, 0, len(entries))
	for _, entry := range entries {
		manifest, err := ReadManifest(entry)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(entry), err)
		}
		manifests = append(manifests, manifest)
	}
	return manifests, nil
}

// Name returns the stable provider name from the validated manifest.
func (a *Adapter) Name() string { return a.manifest.Name }

// Close releases the compiled module and runtime resources.
func (a *Adapter) Close(ctx context.Context) error {
	if a == nil || a.runtime == nil {
		return nil
	}
	return a.runtime.Close(ctx)
}

// Run executes the guest request-planning and response-parsing phases. The
// plugin never receives credentials, raw HTTP access, broker methods, or host
// filesystem paths. The host attaches authentication and validates the target
// path before making the configured provider request.
func (a *Adapter) Run(ctx context.Context, req adapter.Request, broker adapter.Broker) (adapter.Response, error) {
	if a == nil || a.runtime == nil || a.module == nil {
		return adapter.Response{}, errors.New("WASM provider adapter is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return adapter.Response{}, err
	}
	maxTokens := a.manifest.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}
	prepareInput, err := json.Marshal(struct {
		Phase     string `json:"phase"`
		Model     string `json:"model"`
		Prompt    string `json:"prompt"`
		MaxTokens int    `json:"max_tokens"`
	}{"prepare", a.manifest.Model, req.Prompt, maxTokens})
	if err != nil {
		return adapter.Response{}, err
	}
	var providerRequest struct {
		Path string          `json:"path"`
		Body json.RawMessage `json:"body"`
	}
	if err := a.callGuest(ctx, prepareInput, &providerRequest); err != nil {
		return adapter.Response{}, fmt.Errorf("WASM provider prepare phase: %w", err)
	}
	if err := validateRequestPath(providerRequest.Path); err != nil {
		return adapter.Response{}, err
	}
	trimmedBody := bytes.TrimSpace(providerRequest.Body)
	if len(trimmedBody) == 0 || len(trimmedBody) > maxRequestBytes || !json.Valid(trimmedBody) || trimmedBody[0] != '{' {
		return adapter.Response{}, errors.New("WASM provider returned an invalid JSON object request body")
	}
	if err := ctx.Err(); err != nil {
		return adapter.Response{}, err
	}
	headers := make(map[string]string, len(a.manifest.Headers)+1)
	for key, value := range a.manifest.Headers {
		headers[key] = value
	}
	headers[a.manifest.AuthHeader] = a.manifest.AuthPrefix + a.client.APIKey()
	responseBody, err := a.client.PostRawJSON(ctx, providerRequest.Path, headers, trimmedBody)
	if err != nil {
		return adapter.Response{}, fmt.Errorf("WASM provider HTTP request: %w", err)
	}
	responseInput, err := json.Marshal(struct {
		Phase    string          `json:"phase"`
		Response json.RawMessage `json:"response"`
	}{"response", responseBody})
	if err != nil {
		return adapter.Response{}, err
	}
	var result struct {
		Text string `json:"text"`
	}
	if err := a.callGuest(ctx, responseInput, &result); err != nil {
		return adapter.Response{}, fmt.Errorf("WASM provider response phase: %w", err)
	}
	if strings.TrimSpace(result.Text) == "" {
		return adapter.Response{}, errors.New("WASM provider returned empty text")
	}
	if len(result.Text) > maxTextBytes {
		return adapter.Response{}, errors.New("WASM provider text exceeds 1 MiB")
	}
	if err := ctx.Err(); err != nil {
		return adapter.Response{}, err
	}
	return modelplugin.Publish(broker, result.Text)
}

func (a *Adapter) callGuest(parent context.Context, input []byte, output any) error {
	if len(input) > maxGuestIOBytes {
		return errors.New("WASM plugin input exceeds 8 MiB")
	}
	ctx, cancel := context.WithTimeout(parent, guestTimeout)
	defer cancel()
	var stdout limitedBuffer
	stdout.limit = maxGuestIOBytes
	var stderr limitedBuffer
	stderr.limit = 32 << 10
	config := wazero.NewModuleConfig().
		WithStdin(bytes.NewReader(input)).
		WithStdout(&stdout).
		WithStderr(&stderr).
		WithStartFunctions("_start")
	module, err := a.runtime.InstantiateModule(ctx, a.module, config)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("guest execution stopped: %w", ctx.Err())
		}
		return errors.New("guest execution failed")
	}
	defer module.Close(context.Background())
	if len(stdout.data) == 0 {
		return errors.New("guest emitted no JSON output")
	}
	if err := decodeStrict(stdout.data, output); err != nil {
		return fmt.Errorf("guest output must be one strict JSON object: %w", err)
	}
	return nil
}

type limitedBuffer struct {
	data  []byte
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(b.data)+len(p) > b.limit {
		return 0, errors.New("guest output limit exceeded")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func validateManifest(m Manifest) error {
	if !pluginNamePattern.MatchString(m.Name) || strings.TrimSpace(m.Name) != m.Name {
		return errors.New("plugin name must contain only letters, digits, underscore, dot, or hyphen and start with a letter or underscore")
	}
	if m.Module == "" || filepath.Clean(m.Module) == "." {
		return errors.New("plugin module path is required")
	}
	if m.SHA256 != "" {
		if len(m.SHA256) != sha256.Size*2 {
			return errors.New("sha256 must be a 64-character hexadecimal digest")
		}
		if _, err := hex.DecodeString(m.SHA256); err != nil {
			return errors.New("sha256 must be a 64-character hexadecimal digest")
		}
	}
	if !envNamePattern.MatchString(m.APIKeyEnv) {
		return errors.New("api_key_env must be a valid environment variable name")
	}
	endpoint, err := url.Parse(m.BaseURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || strings.ContainsAny(m.BaseURL, "?#") {
		return errors.New("WASI provider base_url must be an absolute HTTPS URL without credentials, query, or fragment")
	}
	if err := validateHeaderName(m.AuthHeader); err != nil {
		return fmt.Errorf("invalid auth_header: %w", err)
	}
	if isReservedHeader(m.AuthHeader) {
		return errors.New("auth_header is reserved")
	}
	if strings.ContainsAny(m.AuthPrefix, "\r\n") {
		return errors.New("auth_prefix must not contain newlines")
	}
	if strings.TrimSpace(m.Model) == "" {
		return errors.New("plugin model is required")
	}
	if m.MaxTokens < 0 || m.MaxTokens > 1_000_000 {
		return errors.New("max_tokens must be between 0 and 1000000")
	}
	for name, value := range m.Headers {
		if err := validateHeaderName(name); err != nil {
			return fmt.Errorf("invalid static header %q: %w", name, err)
		}
		if isReservedHeader(name) || strings.EqualFold(name, m.AuthHeader) {
			return fmt.Errorf("static header %q is reserved or duplicates auth_header", name)
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("static header %q contains a newline", name)
		}
	}
	return nil
}

func validateHeaderName(name string) error {
	if name == "" {
		return errors.New("header name is empty or invalid")
	}
	for _, b := range []byte(name) {
		if !isHeaderTokenByte(b) {
			return errors.New("header name is empty or invalid")
		}
	}
	return nil
}

func isHeaderTokenByte(b byte) bool {
	if b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' {
		return true
	}
	return strings.ContainsRune("!#$%&'*+-.^_`|~", rune(b))
}

func isReservedHeader(name string) bool {
	switch strings.ToLower(name) {
	case "host", "content-length", "connection", "transfer-encoding", "content-type", "accept", "proxy-authorization", "cookie":
		return true
	default:
		return false
	}
}

func validateRequestPath(path string) error {
	if path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\\#") {
		return errors.New("WASM provider request path must be an absolute-path reference on the configured provider origin")
	}
	parsed, err := url.ParseRequestURI(path)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("WASM provider request path must not contain an origin, query, or fragment")
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		decoded, err := url.PathUnescape(segment)
		if err != nil || decoded == "." || decoded == ".." {
			return errors.New("WASM provider request path contains an invalid traversal segment")
		}
	}
	return nil
}

var _ adapter.Adapter = (*Adapter)(nil)
