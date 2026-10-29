// Package discovery resolves identified Peers and maintains runtime presence.
package discovery

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	rv "github.com/scbizu/anra/gen/anra/registry/v1"
	"github.com/scbizu/anra/gen/anra/registry/v1/registryv1connect"
	peermodel "github.com/scbizu/anra/pkg/model/peer"
)

type Client struct {
	registryv1connect.RegistryServiceClient
}

func NewClient(address string) (*Client, error) {
	base, err := peermodel.Address(address)
	if err != nil {
		return nil, fmt.Errorf("discovery: registry must be an HTTP(S) base URL: %w", err)
	}
	h := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	transport := connecthttp.NewTransport(h, base, connecthttp.WithReadMaxBytes(1<<20), connecthttp.WithSendMaxBytes(1<<20))
	return &Client{registryv1connect.NewRegistryServiceClient(connect.NewClient(transport))}, nil
}

func (c *Client) Lookup(ctx context.Context, id string) (*rv.Entry, error) {
	response, err := c.RegistryServiceClient.Lookup(ctx, &rv.LookupRequest{Id: id})
	if err != nil {
		return nil, err
	}
	return response.Entry, nil
}
func (c *Client) List(ctx context.Context, filter ...*rv.ListRequest) ([]*rv.Entry, error) {
	req := &rv.ListRequest{}
	if len(filter) > 0 && filter[0] != nil {
		req = filter[0]
	}
	response, err := c.RegistryServiceClient.List(ctx, req)
	if err != nil {
		return nil, err
	}
	return response.Entry, nil
}

// RequireOnline preserves known-but-unavailable identity instead of treating it
// as missing or silently selecting another agent with a similar skill.
func RequireOnline(entry *rv.Entry) error {
	if entry.GetPresence().GetState() != rv.PresenceState_PRESENCE_STATE_ONLINE {
		state := "unknown"
		if entry.GetPresence().GetState() == rv.PresenceState_PRESENCE_STATE_OFFLINE {
			state = "offline"
		}
		return fmt.Errorf("peer %s is %s", entry.GetPeer().GetId(), state)
	}
	return nil
}
