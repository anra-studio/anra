package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/scbizu/anra/pkg/discovery"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newAskCommand(common *agentOptions, settings *viper.Viper) *cobra.Command {
	var timeout time.Duration
	command := &cobra.Command{Use: "ask [flags] message", Short: "Ask a running agent to use its model and built-in tools", Args: messageArgs,
		PreRunE: func(*cobra.Command, []string) error { return validateTimeout(timeout) },
		RunE: func(command *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(command.Context(), timeout)
			defer cancel()
			directory, err := discovery.NewClient(settings.GetString("registry"))
			if err != nil {
				return err
			}
			record, err := directory.Lookup(ctx, common.id)
			if err != nil {
				return err
			}
			base, err := discovery.BaseURL(record)
			if err != nil {
				return err
			}
			client, err := newPeerClient(ctx, base, common.id)
			if err != nil {
				return err
			}
			if client.Name != common.id {
				return errors.New("discovered card name mismatch")
			}
			answer, err := client.Send(ctx, strings.Join(args, " "))
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), answer)
			return err
		},
	}
	command.Flags().DurationVar(&timeout, "timeout", time.Minute, "command timeout")
	return command
}
