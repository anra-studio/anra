package cmd

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net"
	"net/http"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/agent"
	"github.com/scbizu/anra/pkg/discovery"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newServeCommand(common *agentOptions, settings *viper.Viper) *cobra.Command {
	var model modelOptions
	var listen string
	command := &cobra.Command{
		Use:   "serve",
		Short: "Expose a tape-backed agent over A2A HTTP",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			llm, err := newModel(model, settings.GetString("deepseek-api-key"))
			if err != nil {
				return err
			}
			token, err := peerToken(settings)
			if err != nil {
				return err
			}
			return withAgent(command.Context(), *common, llm, model, settings, func(a *agent.Anra) error {
				return serve(command.Context(), a, common.id, listen, token, settings, command.ErrOrStderr())
			})
		},
	}
	model.flags(command)
	command.Flags().StringVar(&listen, "listen", "127.0.0.1:8080", "HTTP listen address")
	return command
}

func serve(ctx context.Context, a *agent.Anra, id, listen, token string, settings *viper.Viper, status io.Writer) error {
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
	authenticate := func(req *http.Request) (string, error) {
		if subtle.ConstantTimeCompare([]byte(req.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			return "", errors.New("invalid peer token")
		}
		return "trusted-peer", nil
	}
	handler, err := a2a.NewMessageHandler(card, a2asrv.NewHandler(executor), authenticate)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle(outboundPath, outboundHandler(a, settings, authenticate))
	mux.Handle("/", handler)
	registered, cleanup, err := registerAgent(ctx, settings.GetString("registry"), token, discovery.Record{Name: id, CardURL: base + a2asrv.WellKnownAgentCardPath, Card: card})
	if err != nil {
		return err
	}
	defer cleanup()
	err = serveHTTP(registered, listener, mux, id, status)
	if err == nil && ctx.Err() == nil && context.Cause(registered) != nil {
		return context.Cause(registered)
	}
	return err
}
