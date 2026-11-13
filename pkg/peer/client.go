package peer

import (
	"context"

	"github.com/anra-studio/anra/pkg/flow/clbr"

	"github.com/anra-studio/anra/pkg/a2a"
	"github.com/anra-studio/anra/pkg/config"
	"github.com/anra-studio/anra/pkg/discovery"
)

// Ask discovers and calls the configured running agent, without opening local
// storage or constructing a model. Structured BUSY outcomes remain available to
// application shells; transport and cancellation failures are returned as errors.
func Ask(ctx context.Context, settings config.Settings, input string) (clbr.Result, error) {
	if err := settings.Validate(); err != nil {
		return clbr.Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, settings.Timeout)
	defer cancel()
	directory, err := discovery.NewClient(settings.Registry)
	if err != nil {
		return clbr.Result{}, err
	}
	record, err := directory.Lookup(ctx, settings.ID)
	if err != nil {
		return clbr.Result{}, err
	}
	if err := discovery.RequireOnline(record); err != nil {
		return clbr.Result{}, err
	}
	client, err := a2a.NewMessageClient(ctx, a2a.TrustedHTTPClient(settings.ID), record.Peer)
	if err != nil {
		return clbr.Result{}, err
	}
	return client.Send(ctx, input)
}
