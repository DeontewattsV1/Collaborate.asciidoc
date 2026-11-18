// Command openai-wasi is an example third-party provider adapter compiled as
// WASI WebAssembly. It only transforms JSON input/output: it has no network,
// filesystem, API key, or broker access.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type input struct {
	Phase     string          `json:"phase"`
	Model     string          `json:"model,omitempty"`
	Prompt    string          `json:"prompt,omitempty"`
	MaxTokens int             `json:"max_tokens,omitempty"`
	Response  json.RawMessage `json:"response,omitempty"`
}

func main() {
	var in input
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 8<<20))
	if err := decoder.Decode(&in); err != nil {
		fail(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	switch in.Phase {
	case "prepare":
		if in.MaxTokens <= 0 {
			in.MaxTokens = 1024
		}
		out := struct {
			Path string `json:"path"`
			Body any    `json:"body"`
		}{"/chat/completions", map[string]any{
			"model":      in.Model,
			"max_tokens": in.MaxTokens,
			"messages":   []map[string]string{{"role": "user", "content": in.Prompt}},
		}}
		if err := encoder.Encode(out); err != nil {
			fail(err)
		}
	case "response":
		var result struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(in.Response, &result); err != nil {
			fail(err)
		}
		if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
			fail(fmt.Errorf("provider returned no text choices"))
		}
		if err := encoder.Encode(map[string]string{"text": result.Choices[0].Message.Content}); err != nil {
			fail(err)
		}
	default:
		fail(fmt.Errorf("unsupported plugin phase %q", in.Phase))
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
