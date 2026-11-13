package registry

import (
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/validate"
	"connectrpc.com/vanguard"
	"github.com/anra-studio/anra/gen/anra/registry/v1/registryv1connect"
)

// NewHandler mounts the generated Connect and Vanguard REST APIs.
// The caller owns the opened registry.
func NewHandler(registry *Registry) (http.Handler, error) {
	server := connect.NewServer(validate.NewServerInterceptor())
	registryv1connect.RegisterRegistryServiceHandler(server, registry)
	// Registry metadata has its own message policy; media transports do not.
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server, connecthttp.WithReadMaxBytes(MaxMessageBytes), connecthttp.WithSendMaxBytes(MaxMessageBytes))
	if err := vanguard.Mount(mux, server, vanguard.WithMaxReadBytes(MaxMessageBytes)); err != nil {
		return nil, err
	}
	return mux, nil
}
