// Package adapter defines small, broker-aware model/provider adapters.
//
// Implementations receive broker capabilities rather than an HTTP client or
// broker credentials. Provider adapters perform inference in Run and use only
// the broker operations they need. Adapters that modify repository files
// should claim their paths before writing and release them afterward; adapters should
// submit a receipt only when they have a real commit and its metadata.
// Provider credentials and HTTP setup live in provider-specific packages, not
// this interface package.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Turn is one earlier message in a conversation. Role is "user" or "assistant".
type Turn struct {
	Role string
	Text string
}

// Request contains provider-independent input for an adapter.
// System is the character personality. Prior is the thread so far, not
// including Prompt, which is the newest message the model should answer.
type Request struct {
	Prompt string
	System string
	Prior  []Turn
}

// Response is the provider-independent result returned by an adapter.
type Response struct {
	Text string
}

// Adapter performs one deterministic or provider-backed completion. The
// broker parameter exposes only the coordination operations supported by the
// current signed protocol; an adapter need not use every operation.
type Adapter interface {
	Name() string
	Run(context.Context, Request, Broker) (Response, error)
}

var (
	ErrAdapterNotFound = errors.New("adapter not found")
	ErrAdapterExists   = errors.New("adapter already registered")
)

// Registry stores adapters by their stable provider name.
type Registry struct {
	adapters map[string]Adapter
}

// NewRegistry creates an empty adapter registry.
func NewRegistry() *Registry {
	return &Registry{adapters: make(map[string]Adapter)}
}

// Register adds an adapter. Names must be non-empty and unique.
func (r *Registry) Register(a Adapter) error {
	if a == nil {
		return errors.New("cannot register a nil adapter")
	}
	name := strings.TrimSpace(a.Name())
	if name == "" {
		return errors.New("adapter name is required")
	}
	if r.adapters == nil {
		r.adapters = make(map[string]Adapter)
	}
	if _, exists := r.adapters[name]; exists {
		return fmt.Errorf("%w: %s", ErrAdapterExists, name)
	}
	r.adapters[name] = a
	return nil
}

// Get returns a registered adapter by name.
func (r *Registry) Get(name string) (Adapter, error) {
	a, ok := r.adapters[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrAdapterNotFound, name)
	}
	return a, nil
}

// Names returns registered adapter names in deterministic sorted order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.adapters))
	for name := range r.adapters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
