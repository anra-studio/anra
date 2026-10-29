// Anra is the command line shell for the reusable packages under pkg/.
package main

import "github.com/spf13/cobra"

// newRootCommand returns an independent command tree with no shared flag state.
func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "anra",
		Short: "Run tape-backed agents and communicate with peers",
		Long: `Run tape-backed agents and communicate with peers over A2A HTTP.

Agents are configured in the TOML file named by --config; --agent selects the
[agents.<name>] table. Settings are read at startup, so restart a command or
server to apply a change. run/serve use tape-go's ADK runtime, configured
models, and built-in tools. DeepSeek requires DEEPSEEK_API_KEY.
Registry and peer endpoints must be accessible only to trusted team members.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("agent", "anra", "agent name in the config file")
	root.PersistentFlags().String("config", "", "TOML file containing agent settings (required except for registry)")
	root.AddCommand(newRunCommand(), newAskCommand(), newServeCommand(), newRegistryCommand())
	return root
}
