// Package anthropic implements a compiled-in Anthropic Messages API adapter plugin.
package anthropic

import (
	"context"
	"errors"
	"net/http"

	"collaborate/internal/adapter"
	"collaborate/internal/modelplugin"
)

// Adapter calls Anthropic's Messages API and publishes the returned text via
// the authenticated broker capability supplied to Run.
type Adapter struct{ client modelplugin.Client }

func New(baseURL, model, apiKey string, hc *http.Client) (*Adapter, error) {
	client, err := modelplugin.NewClient(baseURL, model, apiKey, hc)
	if err != nil {
		return nil, err
	}
	return &Adapter{client: client}, nil
}

func (Adapter) Name() string { return "anthropic" }

func (a *Adapter) Run(ctx context.Context, req adapter.Request, broker adapter.Broker) (adapter.Response, error) {
	if a == nil {
		return adapter.Response{}, errors.New("anthropic adapter is nil")
	}
	if err := ctx.Err(); err != nil {
		return adapter.Response{}, err
	}
	body := map[string]any{
		"model":      a.client.Model(),
		"max_tokens": 1024,
		"messages":   []map[string]string{{"role": "user", "content": req.Prompt}},
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	headers := map[string]string{
		"x-api-key":         a.client.APIKey(),
		"anthropic-version": "2023-06-01",
	}
	if err := a.client.PostJSON(ctx, "/v1/messages", headers, body, &result); err != nil {
		return adapter.Response{}, err
	}
	text := ""
	for _, block := range result.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	if text == "" {
		return adapter.Response{}, errors.New("Anthropic provider returned no text content")
	}
	if err := ctx.Err(); err != nil {
		return adapter.Response{}, err
	}
	return modelplugin.Publish(broker, text)
}

var _ adapter.Adapter = (*Adapter)(nil)
