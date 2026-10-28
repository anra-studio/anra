package registry

import (
	"context"
	"net"
	"time"

	"github.com/scbizu/anra/internal/httpserver"
)

// Serve runs the directory until cancellation. It owns and closes listener,
// including on configuration errors. ready is optional and receives the bound
// address after HTTP serving begins. NewHandler can also be mounted by a host.
func Serve(ctx context.Context, listener net.Listener, ttl time.Duration, ready func(net.Addr) error) error {
	defer listener.Close()
	handler, err := NewHandler(ttl)
	if err != nil {
		return err
	}
	return httpserver.Serve(ctx, listener, handler, ready)
}
