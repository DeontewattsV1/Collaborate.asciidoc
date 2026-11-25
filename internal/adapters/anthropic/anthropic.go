// Package anthropic implements a compiled-in Anthropic Messages API adapter plugin.
package anthropic

import (
	"context"
	"errors"
	"net/http"
	"strings"

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
	messages, system := anthropicMessages(req)
	body := map[string]any{
		"model":      a.client.Model(),
		"max_tokens": 1024,
		"messages":   messages,
	}
	if system != "" {
		body["system"] = system
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

func anthropicMessages(req adapter.Request) ([]map[string]string, string) {
	type turn struct{ role, text string }
	seq := make([]turn, 0, len(req.Prior)+1)
	for _, prior := range req.Prior {
		role := "user"
		if prior.Role == "assistant" {
			role = "assistant"
		}
		if strings.TrimSpace(prior.Text) == "" {
			continue
		}
		seq = append(seq, turn{role, prior.Text})
	}
	seq = append(seq, turn{"user", req.Prompt})
	merged := make([]turn, 0, len(seq))
	for _, item := range seq {
		if n := len(merged); n > 0 && merged[n-1].role == item.role {
			merged[n-1].text += "\n" + item.text
			continue
		}
		merged = append(merged, item)
	}
	if len(merged) == 0 || merged[0].role != "user" {
		merged = append([]turn{{"user", "(the conversation continues)"}}, merged...)
	}
	out := make([]map[string]string, len(merged))
	for i, item := range merged {
		out[i] = map[string]string{"role": item.role, "content": item.text}
	}
	return out, strings.TrimSpace(req.System)
}

var _ adapter.Adapter = (*Adapter)(nil)
