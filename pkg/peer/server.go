package peer

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
	pv "github.com/anra-studio/anra/gen/anra/peer/v1"
	"github.com/anra-studio/anra/internal/httpserver"
	"github.com/anra-studio/anra/pkg/a2a"
	"github.com/anra-studio/anra/pkg/discovery"
	peermodel "github.com/anra-studio/anra/pkg/model/peer"
)

// Serve advertises the bound listener, maintains its observed presence, and serves
// A2A and TapeView until cancellation or a permanent registry rejection. It owns
// the listener and grant journal. After it returns the caller must Close the node.
// ready is optional and runs once the Peer is registered and HTTP serving begins.
func (n *Node) Serve(ctx context.Context, listener net.Listener, ready func(net.Addr) error) (serveErr error) {
	a, settings := n.agent, n.settings
	id := settings.ID
	defer listener.Close()
	base := "http://" + listener.Addr().String()
	if settings.Address != "" {
		base = settings.Address
	}
	metadata := &pv.Peer{Id: id, Description: "Anra tape-backed agent", Address: base, Skill: []*pv.Skill{
		{Id: peermodel.Chat, Name: "Chat", Description: "Talk to this agent"},
		{Id: peermodel.BorrowContext, Name: "Borrow context", Description: "Request a permitted completed view"},
	}}
	card, err := a2a.Card(metadata)
	if err != nil {
		return err
	}
	n.card.Store(card)
	executor, err := a2a.NewExecutor(a)
	if err != nil {
		return err
	}
	views, err := a2a.NewService(a2a.Config{Source: a.Source(), Owner: id, Tape: a.Tape(), JournalPath: filepath.Join(settings.DataDir, id+".grants.db"), Views: a, Exporter: a2a.ContextExporter{}})
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
	registration, err := directory.Start(ctx, metadata, func(value *pv.Peer) {
		card, err := a2a.Card(value)
		if err == nil {
			n.card.Store(card)
		}
	})
	if err != nil {
		return err
	}
	n.metadataMu.Lock()
	n.registration = registration
	n.metadataMu.Unlock()
	defer func() { registration.Close(); n.metadataMu.Lock(); n.registration = nil; n.metadataMu.Unlock() }()
	registered := registration.Context()
	dynamic := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == a2asrv.WellKnownAgentCardPath {
			a2asrv.NewStaticAgentCardHandler(n.card.Load()).ServeHTTP(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	})
	err = httpserver.Serve(registered, listener, dynamic, ready)
	if err == nil && ctx.Err() == nil && context.Cause(registered) != nil {
		return context.Cause(registered)
	}
	return err
}
