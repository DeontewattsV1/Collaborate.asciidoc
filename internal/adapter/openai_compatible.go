package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OpenAICompatible calls a provider implementing the OpenAI Chat Completions
// HTTP shape. It is deliberately separate from coordination/authentication:
// provider secrets stay in this adapter and broker calls use only Broker.
type OpenAICompatible struct {
	baseURL string
	model   string
	apiKey  string
	http    *http.Client
}

// NewOpenAICompatible validates provider configuration. Plain HTTP is allowed
// only for loopback endpoints so an API key is not sent over a cleartext remote
// connection.
func NewOpenAICompatible(baseURL, model, apiKey string, hc *http.Client) (*OpenAICompatible, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, fmt.Errorf("adapter endpoint must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("adapter endpoint must not contain credentials, query, or fragment")
	}
	if parsed.Scheme == "http" && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1" {
		return nil, fmt.Errorf("cleartext HTTP is permitted only for loopback endpoints")
	}
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("adapter model is required")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("provider API key is required")
	}
	if hc == nil {
		hc = &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &OpenAICompatible{baseURL: baseURL, model: model, apiKey: apiKey, http: hc}, nil
}

func (a *OpenAICompatible) Name() string { return "openai-compatible" }

func (a *OpenAICompatible) Run(ctx context.Context, req Request, broker Broker) (Response, error) {
	if broker == nil {
		return Response{}, fmt.Errorf("openai-compatible adapter: broker is required")
	}
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	body, err := json.Marshal(map[string]any{
		"model":    a.model,
		"messages": []map[string]string{{"role": "user", "content": req.Prompt}},
	})
	if err != nil {
		return Response{}, err
	}
	endpoint := a.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	resp, err := a.http.Do(httpReq)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Response{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Response{}, fmt.Errorf("model provider %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err = json.Unmarshal(data, &result); err != nil {
		return Response{}, fmt.Errorf("decode model response: %w", err)
	}
	if len(result.Choices) == 0 {
		return Response{}, fmt.Errorf("model provider returned no choices")
	}
	response := Response{Text: result.Choices[0].Message.Content}
	if _, err = broker.Say(response.Text); err != nil {
		return Response{}, fmt.Errorf("record model response through broker: %w", err)
	}
	return response, nil
}

var _ Adapter = (*OpenAICompatible)(nil)
