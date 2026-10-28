package a2a

import (
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/scbizu/anra/gen/anra/a2a/tape/v1/tapev1connect"
	pv "github.com/scbizu/anra/gen/anra/peer/v1"
	peermodel "github.com/scbizu/anra/pkg/model/peer"
)

// Card translates Anra's canonical Peer into its A2A representation. Protocol
// paths and extension versions belong to this adapter, never registry metadata.
func Card(value *pv.Peer) (*sdk.AgentCard, error) {
	value, err := peermodel.Normalize(value)
	if err != nil {
		return nil, err
	}
	card := &sdk.AgentCard{Name: value.Id, Description: value.Description, Version: "0.1.0",
		SupportedInterfaces: []*sdk.AgentInterface{sdk.NewAgentInterface(value.Address+CorePath, sdk.TransportProtocolJSONRPC)},
		DefaultInputModes:   []string{"text/plain"}, DefaultOutputModes: []string{"text/plain"},
	}
	for _, skill := range value.Skill {
		card.Skills = append(card.Skills, sdk.AgentSkill{ID: skill.Id, Name: skill.Name, Description: skill.Description, Tags: []string{skill.Id}})
	}
	AdvertiseCollaboration(card)
	if peermodel.HasSkill(value, peermodel.BorrowContext) {
		if err := AdvertisePublications(card, value.Address+ViewPath); err != nil {
			return nil, err
		}
	}
	return card, nil
}

// NewPeerClient uses the typed Peer address directly. Agent Card extension
// interpretation remains available to external A2A callers through NewClient.
func NewPeerClient(h *http.Client, value *pv.Peer) (*Client, error) {
	value, err := peermodel.Normalize(value)
	if err != nil {
		return nil, err
	}
	if h == nil {
		return nil, ErrInvalidParams
	}
	if !peermodel.HasSkill(value, peermodel.BorrowContext) {
		return nil, ErrInactive
	}
	transport := connecthttp.NewTransport(h, value.Address, connecthttp.WithReadMaxBytes(MaxBodyBytes), connecthttp.WithSendMaxBytes(MaxBodyBytes))
	return &Client{rpc: tapev1connect.NewTapeViewServiceClient(connect.NewClient(transport)), publicationVersion: true}, nil
}
