package main

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"time"

	rv "github.com/scbizu/anra/gen/anra/registry/v1"
	"github.com/scbizu/anra/pkg/discovery"
	"github.com/scbizu/anra/pkg/registry"
	"github.com/spf13/cobra"
)

func newRegistryCommand() *cobra.Command {
	var listen string
	var heartbeatTimeout time.Duration
	var dataDir string
	command := &cobra.Command{Use: "registry", Short: "Run the team's persistent Peer directory", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			listener, err := net.Listen("tcp", listen)
			if err != nil {
				return err
			}
			return registry.Serve(command.Context(), listener, registry.Config{Path: filepath.Join(dataDir, "registry.db"), HeartbeatTimeout: heartbeatTimeout}, announce(command, "registry"))
		},
	}
	command.Flags().StringVar(&listen, "listen", "127.0.0.1:9090", "directory HTTP listen address")
	command.Flags().DurationVar(&heartbeatTimeout, "heartbeat-timeout", 15*time.Second, "contact freshness window (at least 1s)")
	command.Flags().StringVar(&dataDir, "data-dir", ".anra", "durable registry storage directory")
	var address string
	forget := &cobra.Command{Use: "forget peer-id", Short: "Explicitly retire an offline or unknown Peer", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(command.Context(), 5*time.Second)
		defer cancel()
		client, err := discovery.NewClient(address)
		if err != nil {
			return err
		}
		entry, err := client.Lookup(ctx, args[0])
		if err != nil {
			return err
		}
		_, err = client.Forget(ctx, &rv.ForgetRequest{Id: args[0], ExpectedRevision: entry.Revision})
		return err
	}}
	forget.Flags().StringVar(&address, "address", "http://127.0.0.1:9090", "registry address")
	command.AddCommand(forget)
	return command
}

// announce adapts a server readiness event to the terminal's status output.
func announce(command *cobra.Command, name string) func(net.Addr) error {
	return func(address net.Addr) error {
		_, err := fmt.Fprintf(command.ErrOrStderr(), "%s listening at http://%s\n", name, address)
		return err
	}
}
