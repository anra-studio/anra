package cmd

import (
	"context"
	"time"

	"github.com/scbizu/anra/pkg/agent"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newRunCommand(common *agentOptions, settings *viper.Viper) *cobra.Command {
	var model modelOptions
	var timeout time.Duration
	command := &cobra.Command{
		Use:     "run [flags] message",
		Short:   "Ask a local tape-backed agent",
		Args:    messageArgs,
		PreRunE: func(*cobra.Command, []string) error { return validateTimeout(timeout) },
		RunE: func(command *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(command.Context(), timeout)
			defer cancel()
			modelRunner, err := newRunner(*common, model, settings.GetString("deepseek-api-key"))
			if err != nil {
				return err
			}
			return withAgent(ctx, *common, modelRunner, model, func(a *agent.Anra) error {
				return printAnswer(ctx, a, args, command.OutOrStdout())
			})
		},
	}
	model.flags(command)
	command.Flags().DurationVar(&timeout, "timeout", time.Minute, "command timeout")
	return command
}
