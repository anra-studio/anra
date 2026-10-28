package peer

import (
	"context"
	"errors"
	"net"
	"path/filepath"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/scbizu/anra/internal/httpserver"
	"github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/discovery"
)

// Serve advertises the bound listener, maintains its directory lease, and serves
// A2A and TapeView until cancellation or a permanent registry rejection. It owns
// the listener and grant journal. After it returns the caller must Close the node.
// ready is optional and runs once the card is registered and HTTP serving begins.
func (n *Node) Serve(ctx context.Context, listener net.Listener, ready func(net.Addr) error) (serveErr error) {
	a, settings := n.agent, n.settings
	id := settings.ID
	defer listener.Close()
	base := "http://" + listener.Addr().String()
	card := &sdk.AgentCard{
		Name: id, Description: "Anra tape-backed agent", Version: "0.1.0",
		SupportedInterfaces: []*sdk.AgentInterface{sdk.NewAgentInterface(base+a2a.CorePath, sdk.TransportProtocolJSONRPC)},
		DefaultInputModes:   []string{"text/plain"}, DefaultOutputModes: []string{"text/plain"},
		Skills: []sdk.AgentSkill{{ID: "chat", Name: "Chat", Description: "Talk to this agent", Tags: []string{"chat"}}},
	}
	executor, err := a2a.NewExecutor(a)
	if err != nil {
		return err
	}
	a2a.AdvertiseCollaboration(card)
	if err := a2a.AdvertisePublications(card, base+a2a.ViewPath); err != nil {
		return err
	}
	views, err := a2a.NewService(a2a.Config{Source: a.Source(), Owner: id, Tape: a.Tape(), JournalPath: filepath.Join(settings.DataDir, id+".grants.db"), Publications: a, Exporter: a2a.PublicationExporter{}})
	if err != nil {
		return err
	}
	defer func() { serveErr = errors.Join(serveErr, views.Close()) }()
	handler, err := a2a.NewPeerHandler(card, a2asrv.NewHandler(executor), views, a2a.TrustedPeerIdentity)
	if err != nil {
		return err
	}
	directory, err := discovery.NewClient(settings.Registry)
	if err != nil {
		return err
	}
	registered, cleanup, err := directory.MaintainRegistration(ctx, discovery.Record{Name: id, CardURL: base + a2asrv.WellKnownAgentCardPath, Card: card})
	if err != nil {
		return err
	}
	defer cleanup()
	err = httpserver.Serve(registered, listener, handler, ready)
	if err == nil && ctx.Err() == nil && context.Cause(registered) != nil {
		return context.Cause(registered)
	}
	return err
}
