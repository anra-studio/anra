package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newSendCommand(common *agentOptions, settings *viper.Viper) *cobra.Command {
	var to string
	var timeout time.Duration
	command := &cobra.Command{
		Use:   "send [flags] message",
		Short: "Ask a running agent to send a message to a named peer",
		Args:  messageArgs,
		PreRunE: func(*cobra.Command, []string) error {
			if err := validateTimeout(timeout); err != nil {
				return err
			}
			if to == "" {
				return errors.New("required flag(s): --to")
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(command.Context(), timeout)
			defer cancel()
			answer, err := sendFromServer(ctx, common.id, to, strings.Join(args, " "), settings)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), answer)
			return err
		},
	}
	command.Flags().StringVar(&to, "to", "", "registered destination agent name (source must be serving)")
	command.Flags().DurationVar(&timeout, "timeout", time.Minute, "command timeout")
	return command
}
