// Package modelplugin contains shared, provider-neutral utilities for compiled-in model adapters.
package modelplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"collaborate/internal/adapter"
)

const maxResponseBytes = 8 << 20

// Client is a bounded HTTP client configured for one model endpoint and secret.
// It never follows redirects, preventing provider credentials from being sent
// to a different host.
type Client struct {
	baseURL string
	model   string
	apiKey  string
	http    *http.Client
}

// NewClient validates provider configuration. Cleartext HTTP is allowed only
// for loopback endpoints used in local testing. A supplied client's transport
// may be customized, but redirects are always disabled.
func NewClient(baseURL, model, apiKey string, hc *http.Client) (Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	apiKey = strings.TrimSpace(apiKey)
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return Client{}, errors.New("provider endpoint must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Client{}, errors.New("provider endpoint must not contain credentials, query, or fragment")
	}
	if parsed.Scheme == "http" && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1" {
		return Client{}, errors.New("cleartext HTTP is permitted only for loopback endpoints")
	}
	if strings.TrimSpace(model) == "" {
		return Client{}, errors.New("provider model is required")
	}
	if apiKey == "" || strings.ContainsAny(apiKey, "\r\n") {
		return Client{}, errors.New("provider API key is required")
	}
	if hc == nil {
		hc = &http.Client{}
	} else {
		copy := *hc
		hc = &copy
	}
	if hc.Timeout <= 0 {
		hc.Timeout = 2 * time.Minute
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return Client{baseURL: baseURL, model: strings.TrimSpace(model), apiKey: apiKey, http: hc}, nil
}

// Model returns the configured provider model identifier.
func (c Client) Model() string { return c.model }

// APIKey returns the configured provider secret for constructing the provider's
// required authentication header. Callers must never log or persist its value.
func (c Client) APIKey() string { return c.apiKey }

// PostJSON performs a JSON POST and decodes a bounded JSON response. Provider
// error bodies are deliberately omitted from returned errors to avoid leaking
// prompts or other provider response content into logs.
func (c Client) PostJSON(ctx context.Context, path string, headers map[string]string, requestBody, responseBody any) error {
	if c.http == nil {
		return errors.New("provider client is not initialized")
	}
	body, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("encode provider request: %w", err)
	}
	data, err := c.PostRawJSON(ctx, path, headers, body)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, responseBody); err != nil {
		return fmt.Errorf("decode provider response: %w", err)
	}
	return nil
}

// PostRawJSON sends an already-encoded JSON request and returns the bounded raw
// JSON response. It applies the same redirect, timeout, status, and body limits
// as PostJSON, and is used by sandboxed provider plugins whose request shape is
// constructed in WASI while the host retains all network and credential access.
func (c Client) PostRawJSON(ctx context.Context, path string, headers map[string]string, body []byte) ([]byte, error) {
	if c.http == nil {
		return nil, errors.New("provider client is not initialized")
	}
	if !json.Valid(body) {
		return nil, errors.New("provider request body is not valid JSON")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create provider request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("provider request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read provider response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("provider response exceeds %d bytes", maxResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("provider returned HTTP %s", resp.Status)
	}
	if !json.Valid(data) {
		return nil, errors.New("provider response is not valid JSON")
	}
	return data, nil
}

// Publish records model output by calling the authenticated broker capability.
func Publish(broker adapter.Broker, text string) (adapter.Response, error) {
	if broker == nil {
		return adapter.Response{}, errors.New("broker is required")
	}
	if _, err := broker.Say(text); err != nil {
		return adapter.Response{}, fmt.Errorf("record model response through broker: %w", err)
	}
	return adapter.Response{Text: text}, nil
}
