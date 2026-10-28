package registry

import (
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/validate"
	"github.com/scbizu/anra/gen/anra/registry/v1/registryv1connect"
	"net/http"
)

// NewHandler mounts the generated API. The caller owns the opened registry.
func NewHandler(registry *Registry) http.Handler {
	server := connect.NewServer(validate.NewServerInterceptor())
	registryv1connect.RegisterRegistryServiceHandler(server, registry)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server, connecthttp.WithReadMaxBytes(MaxMessageBytes), connecthttp.WithSendMaxBytes(MaxMessageBytes))
	return mux
}
