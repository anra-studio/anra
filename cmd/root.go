// Package cmd defines the Anra command line interface.
package cmd

import "github.com/spf13/cobra"

// NewRootCommand returns an independent command tree with no shared flag state.
func NewRootCommand() *cobra.Command {
	var common agentOptions
	settings := newEnvironment()
	root := &cobra.Command{
		Use:   "anra",
		Short: "Run tape-backed agents and communicate with peers",
		Long: `Run tape-backed agents and communicate with peers over A2A HTTP.

run/serve use DeepSeek (DEEPSEEK_API_KEY) by default; --runner echo needs no key.
serve/send require the same ANRA_PEER_TOKEN environment variable.
Options also accept ANRA_ environment variables, such as ANRA_AGENT and ANRA_RUNNER.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(command *cobra.Command, _ []string) error {
			if err := applyConfig(command, settings); err != nil {
				return err
			}
			return common.validate()
		},
	}
	root.PersistentFlags().StringVar(&common.id, "agent", "anra", "local agent identity")
	root.PersistentFlags().StringVar(&common.dataDir, "data-dir", ".anra", "directory for per-agent tape databases")
	root.PersistentFlags().StringVar(&common.session, "session", "default", "persistent conversation session")
	root.PersistentFlags().String("config", "", "TOML file containing agent settings")
	root.PersistentFlags().String("registry", "", "team Agent Card directory URL")
	root.AddCommand(newRunCommand(&common, settings), newServeCommand(&common, settings), newSendCommand(&common, settings), newRegistryCommand(settings), newPeersCommand(settings))
	return root
}
