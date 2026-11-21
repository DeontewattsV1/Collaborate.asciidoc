// Package openai implements a compiled-in OpenAI Chat Completions adapter plugin.
package openai

import (
	"context"
	"errors"
	"net/http"

	"collaborate/internal/adapter"
	"collaborate/internal/modelplugin"
)

// Adapter calls an OpenAI-compatible Chat Completions endpoint and publishes
// the resulting text through the signed broker capability supplied to Run.
type Adapter struct{ client modelplugin.Client }

func New(baseURL, model, apiKey string, hc *http.Client) (*Adapter, error) {
	client, err := modelplugin.NewClient(baseURL, model, apiKey, hc)
	if err != nil {
		return nil, err
	}
	return &Adapter{client: client}, nil
}

func (Adapter) Name() string { return "openai-compatible" }

func (a *Adapter) Run(ctx context.Context, req adapter.Request, broker adapter.Broker) (adapter.Response, error) {
	if a == nil {
		return adapter.Response{}, errors.New("openai adapter is nil")
	}
	if err := ctx.Err(); err != nil {
		return adapter.Response{}, err
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	body := map[string]any{
		"model":    a.client.Model(),
		"messages": []map[string]string{{"role": "user", "content": req.Prompt}},
	}
	if err := a.client.PostJSON(ctx, "/chat/completions", map[string]string{"Authorization": "Bearer " + a.client.APIKey()}, body, &result); err != nil {
		return adapter.Response{}, err
	}
	if len(result.Choices) == 0 {
		return adapter.Response{}, errors.New("OpenAI-compatible provider returned no choices")
	}
	text := result.Choices[0].Message.Content
	if text == "" {
		return adapter.Response{}, errors.New("OpenAI-compatible provider returned empty text content")
	}
	if err := ctx.Err(); err != nil {
		return adapter.Response{}, err
	}
	return modelplugin.Publish(broker, text)
}

var _ adapter.Adapter = (*Adapter)(nil)
