package cmd

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/agent"
	"github.com/spf13/cobra"
)

func newServeCommand(common *agentOptions) *cobra.Command {
	var model modelOptions
	var listen string
	command := &cobra.Command{
		Use:   "serve",
		Short: "Expose a tape-backed agent over A2A HTTP",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			modelRunner, err := newRunner(*common, model)
			if err != nil {
				return err
			}
			token, err := peerToken()
			if err != nil {
				return err
			}
			return withAgent(command.Context(), *common, modelRunner, model, func(a *agent.Anra) error {
				return serve(command.Context(), a, common.id, listen, token, command.ErrOrStderr())
			})
		},
	}
	model.flags(command)
	command.Flags().StringVar(&listen, "listen", "127.0.0.1:8080", "HTTP listen address")
	return command
}

func serve(ctx context.Context, a *agent.Anra, id, listen, token string, status io.Writer) error {
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	base := "http://" + listener.Addr().String()
	card := &sdk.AgentCard{
		Name: id, Description: "Anra tape-backed agent", Version: "0.1.0",
		SupportedInterfaces: []*sdk.AgentInterface{sdk.NewAgentInterface(base+a2a.CorePath, sdk.TransportProtocolJSONRPC)},
		DefaultInputModes:   []string{"text/plain"}, DefaultOutputModes: []string{"text/plain"},
		Skills:               []sdk.AgentSkill{{ID: "chat", Name: "Chat", Description: "Talk to this agent", Tags: []string{"chat"}}},
		SecuritySchemes:      sdk.NamedSecuritySchemes{"bearer": sdk.HTTPAuthSecurityScheme{Scheme: "Bearer"}},
		SecurityRequirements: sdk.SecurityRequirementsOptions{{"bearer": {}}},
	}
	executor, err := a2a.NewExecutor(a)
	if err != nil {
		return err
	}
	handler, err := a2a.NewMessageHandler(card, a2asrv.NewHandler(executor), func(req *http.Request) (string, error) {
		if subtle.ConstantTimeCompare([]byte(req.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			return "", errors.New("invalid peer token")
		}
		return "trusted-peer", nil
	})
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer server.Close()
	if _, err := fmt.Fprintf(status, "%s listening at %s\n", id, base); err != nil {
		return err
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
