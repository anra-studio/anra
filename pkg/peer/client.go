package peer

import (
	"context"

	"github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/config"
	"github.com/scbizu/anra/pkg/discovery"
)

// Ask discovers and calls the configured running agent, without opening local
// storage or constructing a model. Structured BUSY outcomes remain available to
// application shells; transport and cancellation failures are returned as errors.
func Ask(ctx context.Context, settings config.Settings, input string) (a2a.CollaborationResult, error) {
	if err := settings.Validate(); err != nil {
		return a2a.CollaborationResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, settings.Timeout)
	defer cancel()
	directory, err := discovery.NewClient(settings.Registry)
	if err != nil {
		return a2a.CollaborationResult{}, err
	}
	record, err := directory.Lookup(ctx, settings.ID)
	if err != nil {
		return a2a.CollaborationResult{}, err
	}
	client, err := a2a.NewMessageClient(ctx, a2a.TrustedHTTPClient(settings.ID), record)
	if err != nil {
		return a2a.CollaborationResult{}, err
	}
	return client.Send(ctx, input)
}
