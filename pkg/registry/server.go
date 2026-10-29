package registry

import (
	"context"
	"errors"
	"net"

	"github.com/anra-studio/anra/internal/httpserver"
)

// Serve owns the listener and durable directory until cancellation.
func Serve(ctx context.Context, listener net.Listener, config Config, ready func(net.Addr) error) (serveErr error) {
	defer listener.Close()
	registry, err := Open(config)
	if err != nil {
		return err
	}
	defer func() { serveErr = errors.Join(serveErr, registry.Close()) }()
	handler, err := NewHandler(registry)
	if err != nil {
		return err
	}
	return httpserver.Serve(ctx, listener, handler, ready)
}
