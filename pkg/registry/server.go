package registry

import (
	"context"
	"errors"
	"github.com/scbizu/anra/internal/httpserver"
	"net"
)

// Serve owns the listener and durable directory until cancellation.
func Serve(ctx context.Context, listener net.Listener, config Config, ready func(net.Addr) error) (serveErr error) {
	defer listener.Close()
	registry, err := Open(config)
	if err != nil {
		return err
	}
	defer func() { serveErr = errors.Join(serveErr, registry.Close()) }()
	return httpserver.Serve(ctx, listener, NewHandler(registry), ready)
}
