// Package httpserver exposes RPC/REST routes and the HTTP lifecycle shared by peer and registry.
package httpserver

import (
	"context"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/vanguard"
)

// NewHandler exposes the same RPC implementations through Connect and
// schema-defined REST routes. Both transports run the server's interceptors.
func NewHandler(server *connect.Server, maxBytes int) (http.Handler, error) {
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server, connecthttp.WithReadMaxBytes(maxBytes), connecthttp.WithSendMaxBytes(maxBytes))
	if err := vanguard.Mount(mux, server, vanguard.WithMaxReadBytes(int64(maxBytes))); err != nil {
		return nil, err
	}
	return mux, nil
}

// Serve owns listener and closes it on return. ready runs after serving begins.
func Serve(ctx context.Context, listener net.Listener, handler http.Handler, ready func(net.Addr) error) error {
	defer listener.Close()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer server.Close()
	if ready != nil {
		if err := ready(listener.Addr()); err != nil {
			return err
		}
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
