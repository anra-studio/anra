package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/scbizu/anra/pkg/peer"
	"github.com/spf13/cobra"
)

func newRunCommand() *cobra.Command {
	return &cobra.Command{
		Use: "run [flags] message", Short: "Ask a local tape-backed agent", Args: messageArgs,
		RunE: func(command *cobra.Command, args []string) (runErr error) {
			settings, err := loadSettings(command)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(command.Context(), settings.Timeout)
			defer cancel()
			node, err := peer.Open(ctx, settings)
			if err != nil {
				return err
			}
			defer func() { runErr = errors.Join(runErr, closeNode(node)) }()
			answer, err := node.Run(ctx, message(args))
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), answer)
			return err
		},
	}
}
