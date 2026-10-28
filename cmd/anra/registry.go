package main

import (
	"fmt"
	"net"
	"time"

	"github.com/scbizu/anra/pkg/registry"
	"github.com/spf13/cobra"
)

func newRegistryCommand() *cobra.Command {
	var listen string
	var leaseTTL time.Duration
	command := &cobra.Command{Use: "registry", Short: "Run the team's Agent Card directory", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			listener, err := net.Listen("tcp", listen)
			if err != nil {
				return err
			}
			return registry.Serve(command.Context(), listener, leaseTTL, announce(command, "registry"))
		},
	}
	command.Flags().StringVar(&listen, "listen", "127.0.0.1:9090", "directory HTTP listen address")
	command.Flags().DurationVar(&leaseTTL, "lease-ttl", 15*time.Second, "registration lifetime without renewal (at least 1s)")
	return command
}

// announce adapts a server readiness event to the terminal's status output.
func announce(command *cobra.Command, name string) func(net.Addr) error {
	return func(address net.Addr) error {
		_, err := fmt.Fprintf(command.ErrOrStderr(), "%s listening at http://%s\n", name, address)
		return err
	}
}
