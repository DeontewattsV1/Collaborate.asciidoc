package adapter

import (
	"context"
	"errors"
)

// Echo is a deterministic fixture adapter useful for tests and protocol demos.
// It returns the prompt verbatim and publishes that text as a broker message;
// it makes no model calls and has no provider credentials.
type Echo struct{}

// Name is the stable registry key for this fixture.
func (Echo) Name() string { return "echo" }

// Run echoes the prompt and records the exact response through the broker.
func (Echo) Run(ctx context.Context, req Request, broker Broker) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	if broker == nil {
		return Response{}, errors.New("echo adapter: broker is required")
	}
	response := Response{Text: req.Prompt}
	if _, err := broker.Say(response.Text); err != nil {
		return Response{}, err
	}
	return response, nil
}

var _ Adapter = Echo{}
