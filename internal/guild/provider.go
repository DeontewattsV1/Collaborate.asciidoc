package guild

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"collaborate/internal/adapter"
	"collaborate/internal/adapters/anthropic"
	"collaborate/internal/adapters/gemini"
	"collaborate/internal/adapters/openai"
)

// Job is one character's turn.
type Job struct {
	Character Character
	Thread    string
	System    string
	Prompt    string
	Prior     []adapter.Turn
}

// Result is the reply. Recorded is true when the adapter already stored it
// through Broker.Say.
type Result struct {
	Text     string
	Tokens   int
	Recorded bool
}

// Completer produces one in-character reply.
type Completer interface {
	Complete(context.Context, Job) (Result, error)
}

type providerSpec struct {
	kind, base, model, key string
}

func lookup(getenv func(string) string, key string) string {
	if getenv == nil || key == "" {
		return ""
	}
	return strings.TrimSpace(getenv(key))
}

// firstHouse picks the provider that voices tool mascots.
func firstHouse(getenv func(string) string) (providerSpec, bool) {
	if key := lookup(getenv, "ANTHROPIC_API_KEY"); key != "" {
		model := lookup(getenv, "ANTHROPIC_MODEL")
		if model == "" {
			model = "claude-sonnet-5-5"
		}
		base := lookup(getenv, "ANTHROPIC_BASE_URL")
		if base == "" {
			base = "https://api.anthropic.com"
		}
		return providerSpec{kind: "anthropic", base: base, model: model, key: key}, true
	}
	if key := lookup(getenv, "OPENAI_API_KEY"); key != "" {
		model := lookup(getenv, "OPENAI_MODEL")
		if model == "" {
			model = "gpt-6.2-sol"
		}
		base := lookup(getenv, "OPENAI_BASE_URL")
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		return providerSpec{kind: "openai", base: base, model: model, key: key}, true
	}
	if key := firstNonEmpty(lookup(getenv, "GEMINI_API_KEY"), lookup(getenv, "GOOGLE_API_KEY")); key != "" {
		model := lookup(getenv, "GEMINI_MODEL")
		if model == "" {
			model = "gemini-3.8-flash"
		}
		base := lookup(getenv, "GEMINI_BASE_URL")
		if base == "" {
			base = "https://generativelanguage.googleapis.com"
		}
		return providerSpec{kind: "gemini", base: base, model: model, key: key}, true
	}
	if key := lookup(getenv, "XAI_API_KEY"); key != "" {
		model := lookup(getenv, "XAI_MODEL")
		if model == "" {
			model = "grok-4.7"
		}
		base := lookup(getenv, "XAI_BASE_URL")
		if base == "" {
			base = "https://api.x.ai/v1"
		}
		return providerSpec{kind: "openai", base: base, model: model, key: key}, true
	}
	return providerSpec{}, false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func resolveProvider(c Character, getenv func(string) string) (providerSpec, error) {
	if c.Provider == "house" {
		spec, ok := firstHouse(getenv)
		if !ok {
			return providerSpec{}, fmt.Errorf("%s has no house model key", c.Name)
		}
		return spec, nil
	}
	if c.Provider == "local" {
		return providerSpec{}, fmt.Errorf("%s speaks locally", c.Name)
	}
	key := lookup(getenv, c.EnvKey)
	if key == "" {
		key = lookup(getenv, c.AltKey)
	}
	if c.Provider == "ollama" {
		// Ollama's local server does not use a secret. The adapter still
		// requires a non-empty placeholder, and it never leaves the process.
		if key == "" {
			return providerSpec{}, fmt.Errorf("OLLAMA_HOST is not set")
		}
		model := lookup(getenv, c.ModelEnv)
		if model == "" {
			model = c.DefaultModel
		}
		return providerSpec{kind: "openai", base: strings.TrimRight(key, "/") + "/v1", model: model, key: "ollama"}, nil
	}
	if key == "" {
		return providerSpec{}, fmt.Errorf("%s is offline", c.Name)
	}
	model := lookup(getenv, c.ModelEnv)
	if model == "" {
		model = c.DefaultModel
	}
	base := lookup(getenv, c.BaseEnv)
	if base == "" {
		base = c.DefaultBase
	}
	if base == "" {
		return providerSpec{}, fmt.Errorf("%s needs %s", c.Name, c.BaseEnv)
	}
	switch c.Provider {
	case "anthropic":
		return providerSpec{kind: "anthropic", base: base, model: model, key: key}, nil
	case "gemini":
		return providerSpec{kind: "gemini", base: base, model: model, key: key}, nil
	case "openai", "xai", "compatible":
		return providerSpec{kind: "openai", base: base, model: model, key: key}, nil
	default:
		return providerSpec{}, fmt.Errorf("unknown provider %q", c.Provider)
	}
}

// ledgerBroker is the adapter.Broker the model call uses. Say is the only
// capability, and it writes the signed chat log. The others stay closed so a
// chat reply cannot claim paths or write the repo.
type ledgerBroker struct {
	onSay func(string) (adapter.EventAck, error)
}

func (b *ledgerBroker) Say(text string) (adapter.EventAck, error) {
	if b == nil || b.onSay == nil {
		return adapter.EventAck{}, fmt.Errorf("guild broker is not configured")
	}
	return b.onSay(text)
}

func (b *ledgerBroker) Claim(string, []string) (adapter.EventAck, error) {
	return adapter.EventAck{}, fmt.Errorf("guild chat cannot claim paths")
}
func (b *ledgerBroker) Write(string, []byte) (adapter.EventAck, error) {
	return adapter.EventAck{}, fmt.Errorf("guild chat cannot write files")
}
func (b *ledgerBroker) Release([]string) (adapter.EventAck, error) {
	return adapter.EventAck{}, fmt.Errorf("guild chat cannot release leases")
}
func (b *ledgerBroker) Submit(adapter.Receipt) (adapter.EventAck, error) {
	return adapter.EventAck{}, fmt.Errorf("guild chat cannot submit receipts")
}

var _ adapter.Broker = (*ledgerBroker)(nil)

// LiveCompleter calls the configured provider. onSay runs inside Broker.Say
// so the reply is stored by the same broker path the adapters already use.
type LiveCompleter struct {
	Getenv func(string) string
	OnSay  func(character Character, text string, tokens int) (adapter.EventAck, error)
	HTTP   *http.Client
}

func (l LiveCompleter) Complete(ctx context.Context, job Job) (Result, error) {
	getenv := l.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if job.Character.Provider == "local" {
		return Result{}, fmt.Errorf("local character")
	}
	spec, err := resolveProvider(job.Character, getenv)
	if err != nil {
		return Result{}, err
	}
	var impl adapter.Adapter
	switch spec.kind {
	case "anthropic":
		impl, err = anthropic.New(spec.base, spec.model, spec.key, l.HTTP)
	case "gemini":
		impl, err = gemini.New(spec.base, spec.model, spec.key, l.HTTP)
	default:
		impl, err = openai.New(spec.base, spec.model, spec.key, l.HTTP)
	}
	if err != nil {
		return Result{}, err
	}
	tokens := estimateTokens(job.System, job.Prompt)
	broker := &ledgerBroker{onSay: func(text string) (adapter.EventAck, error) {
		text = clipBytes(strings.TrimSpace(text), 4000)
		if text == "" {
			return adapter.EventAck{}, fmt.Errorf("empty model reply")
		}
		if l.OnSay == nil {
			return adapter.EventAck{}, fmt.Errorf("guild broker is not configured")
		}
		return l.OnSay(job.Character, text, tokens+estimateTokens(text))
	}}
	resp, err := impl.Run(ctx, adapter.Request{Prompt: job.Prompt, System: job.System, Prior: job.Prior}, broker)
	if err != nil {
		return Result{}, err
	}
	return Result{Text: resp.Text, Tokens: tokens + estimateTokens(resp.Text), Recorded: true}, nil
}
