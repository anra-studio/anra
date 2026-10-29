package main

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/anra-studio/anra/pkg/peer"
	"github.com/spf13/cobra"
)

func newServeCommand() *cobra.Command {
	return &cobra.Command{
		Use: "serve", Short: "Expose a tape-backed agent over A2A HTTP", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) (runErr error) {
			settings, err := loadSettings(command)
			if err != nil {
				return err
			}
			node, err := peer.Open(command.Context(), settings)
			if err != nil {
				return err
			}
			defer func() { runErr = errors.Join(runErr, closeNode(node)) }()
			listener, err := net.Listen("tcp", settings.Endpoint)
			if err != nil {
				return err
			}
			return node.Serve(command.Context(), listener, announce(command, settings.ID))
		},
	}
}

func closeNode(node *peer.Node) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return node.Close(ctx)
}
