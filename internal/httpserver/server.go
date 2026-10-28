// Package httpserver implements the HTTP lifecycle shared by peer and registry.
package httpserver

import (
	"context"
	"net"
	"net/http"
	"time"
)

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
