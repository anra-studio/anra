package registry

import (
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/validate"
	"github.com/anra-studio/anra/gen/anra/registry/v1/registryv1connect"
	"github.com/anra-studio/anra/internal/httpserver"
)

// NewHandler mounts the generated Connect and Vanguard REST APIs.
// The caller owns the opened registry.
func NewHandler(registry *Registry) (http.Handler, error) {
	server := connect.NewServer(validate.NewServerInterceptor())
	registryv1connect.RegisterRegistryServiceHandler(server, registry)
	return httpserver.NewHandler(server, MaxMessageBytes)
}
