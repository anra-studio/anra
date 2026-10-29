package main

import (
	"fmt"
	"strings"

	cp "github.com/anra-studio/anra/gen/anra/a2a/collaboration/v1"
	"github.com/anra-studio/anra/pkg/peer"
	"github.com/spf13/cobra"
)

func newAskCommand() *cobra.Command {
	return &cobra.Command{Use: "ask [flags] message", Short: "Ask a running agent to use its model and built-in tools", Args: messageArgs,
		RunE: func(command *cobra.Command, args []string) error {
			settings, err := loadSettings(command)
			if err != nil {
				return err
			}
			answer, err := peer.Ask(command.Context(), settings, message(args))
			if err != nil {
				return err
			}
			if answer.Status != cp.Status_STATUS_COMPLETED {
				return fmt.Errorf("%s: request was not accepted", strings.ToLower(strings.TrimPrefix(answer.Status.String(), "STATUS_")))
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), answer.Answer)
			return err
		},
	}
}
