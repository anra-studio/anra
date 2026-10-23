// Package cmd defines the Anra command line interface.
package cmd

import "github.com/spf13/cobra"

// NewRootCommand returns an independent command tree with no shared flag state.
func NewRootCommand() *cobra.Command {
	var common agentOptions
	var configPath string
	root := &cobra.Command{
		Use:   "anra",
		Short: "Run tape-backed agents and communicate with peers",
		Long: `Run tape-backed agents and communicate with peers over A2A HTTP.

run/serve use DeepSeek (DEEPSEEK_API_KEY) by default; --runner echo needs no key.
serve/send require the same ANRA_PEER_TOKEN environment variable.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(command *cobra.Command, _ []string) error {
			if err := applyConfig(command, configPath); err != nil {
				return err
			}
			return common.validate()
		},
	}
	root.PersistentFlags().StringVar(&common.id, "agent", "anra", "local agent identity")
	root.PersistentFlags().StringVar(&common.dataDir, "data-dir", ".anra", "directory for per-agent tape databases")
	root.PersistentFlags().StringVar(&common.session, "session", "default", "persistent conversation session")
	root.PersistentFlags().StringVar(&configPath, "config", "", "TOML file containing agent settings")
	root.AddCommand(newRunCommand(&common), newServeCommand(&common), newSendCommand(&common))
	return root
}
