// Package gemini implements a compiled-in Google Gemini generateContent adapter plugin.
package gemini

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"collaborate/internal/adapter"
	"collaborate/internal/modelplugin"
)

// Adapter calls Gemini's generateContent API and publishes the returned text
// through the authenticated broker capability supplied to Run.
type Adapter struct{ client modelplugin.Client }

func New(baseURL, model, apiKey string, hc *http.Client) (*Adapter, error) {
	client, err := modelplugin.NewClient(baseURL, model, apiKey, hc)
	if err != nil {
		return nil, err
	}
	return &Adapter{client: client}, nil
}

func (Adapter) Name() string { return "gemini" }

func (a *Adapter) Run(ctx context.Context, req adapter.Request, broker adapter.Broker) (adapter.Response, error) {
	if a == nil {
		return adapter.Response{}, errors.New("gemini adapter is nil")
	}
	if err := ctx.Err(); err != nil {
		return adapter.Response{}, err
	}
	path := "/v1beta/models/" + url.PathEscape(a.client.Model()) + ":generateContent"
	body := map[string]any{"contents": geminiContents(req)}
	if system := strings.TrimSpace(req.System); system != "" {
		body["systemInstruction"] = map[string]any{
			"parts": []map[string]string{{"text": system}},
		}
	}
	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := a.client.PostJSON(ctx, path, map[string]string{"x-goog-api-key": a.client.APIKey()}, body, &result); err != nil {
		return adapter.Response{}, err
	}
	if len(result.Candidates) == 0 {
		return adapter.Response{}, errors.New("Gemini provider returned no candidates")
	}
	text := ""
	for _, part := range result.Candidates[0].Content.Parts {
		text += part.Text
	}
	if text == "" {
		return adapter.Response{}, errors.New("Gemini provider returned no text content")
	}
	if err := ctx.Err(); err != nil {
		return adapter.Response{}, err
	}
	return modelplugin.Publish(broker, text)
}

func geminiContents(req adapter.Request) []map[string]any {
	type turn struct{ role, text string }
	seq := make([]turn, 0, len(req.Prior)+1)
	for _, prior := range req.Prior {
		role := "user"
		if prior.Role == "assistant" {
			role = "model"
		}
		if strings.TrimSpace(prior.Text) == "" {
			continue
		}
		seq = append(seq, turn{role, prior.Text})
	}
	seq = append(seq, turn{"user", req.Prompt})
	out := make([]map[string]any, 0, len(seq))
	for _, item := range seq {
		if n := len(out); n > 0 && out[n-1]["role"] == item.role {
			parts := out[n-1]["parts"].([]map[string]string)
			parts[0]["text"] += "\n" + item.text
			out[n-1]["parts"] = parts
			continue
		}
		out = append(out, map[string]any{
			"role":  item.role,
			"parts": []map[string]string{{"text": item.text}},
		})
	}
	return out
}

var _ adapter.Adapter = (*Adapter)(nil)
