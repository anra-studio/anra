package a2a

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	cp "github.com/anra-studio/anra/gen/anra/a2a/collaboration/v1"
	pv "github.com/anra-studio/anra/gen/anra/peer/v1"
	"github.com/anra-studio/anra/pkg/model/delegator"
)

type MessageClient struct {
	client        *a2aclient.Client
	Name          string
	collaboration bool
	Card          *sdk.AgentCard
}

// NewMessageClient resolves protocol details from canonical Peer metadata.
func NewMessageClient(ctx context.Context, h *http.Client, value *pv.Peer) (*MessageClient, error) {
	card, err := Card(value)
	if err != nil {
		return nil, err
	}
	return NewMessageClientFromCard(ctx, h, card)
}

// NewMessageClientFromCard is the external A2A boundary for callers holding an
// Agent Card. Internal discovery uses NewMessageClient with typed Peer values.
func NewMessageClientFromCard(ctx context.Context, h *http.Client, card *sdk.AgentCard) (*MessageClient, error) {
	if h == nil || card == nil || len(card.SupportedInterfaces) == 0 || card.SupportedInterfaces[0] == nil {
		return nil, ErrInvalidParams
	}
	origin, err := url.Parse(card.SupportedInterfaces[0].URL)
	if err != nil || origin.Host == "" || (origin.Scheme != "http" && origin.Scheme != "https") || origin.User != nil {
		return nil, ErrInvalidParams
	}
	base := origin.Scheme + "://" + origin.Host
	for _, endpoint := range card.SupportedInterfaces {
		if endpoint == nil {
			return nil, ErrInvalidParams
		}
		u, err := url.Parse(endpoint.URL)
		if err != nil || u.User != nil || u.Scheme+"://"+u.Host != base {
			return nil, ErrInvalidParams
		}
	}
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != ExtensionURI {
			continue
		}
		endpoint, ok := ext.Params["endpoint"].(string)
		if !ok {
			return nil, ErrInvalidParams
		}
		remote, err := url.Parse(endpoint)
		if err != nil || remote.User != nil || remote.RawQuery != "" || remote.Fragment != "" || remote.Scheme+"://"+remote.Host != base {
			return nil, errors.New("peer advertised a TapeView endpoint outside its origin")
		}
	}
	client, err := a2aclient.NewFromCard(ctx, card, a2aclient.WithJSONRPCTransport(h))
	if err != nil {
		return nil, err
	}
	return &MessageClient{client: client, Name: card.Name, collaboration: advertisesCollaboration(card), Card: card}, nil
}

// Send returns a typed outcome when the peer advertises collaboration.
func (r *MessageClient) Send(ctx context.Context, input string) (CollaborationResult, error) {
	request := sdk.NewMessage(sdk.MessageRoleUser, sdk.NewTextPart(input))
	if r.collaboration {
		request.Extensions = []string{CollaborationURI}
		metadata, err := encodeCollaborationMetadata(delegator.FromContext(ctx).Metadata())
		if err != nil {
			return CollaborationResult{}, err
		}
		request.Metadata = map[string]any{CollaborationURI: metadata}
	}
	result, err := r.client.SendMessage(ctx, &sdk.SendMessageRequest{Message: request})
	if err != nil {
		return CollaborationResult{}, err
	}
	message, ok := result.(*sdk.Message)
	if !ok || message.Role != sdk.MessageRoleAgent {
		return CollaborationResult{}, errors.New("peer returned no direct answer")
	}
	var parts []string
	for _, part := range message.Parts {
		if part == nil {
			return CollaborationResult{}, ErrInvalidParams
		}
		if _, ok := part.Content.(sdk.Text); !ok {
			return CollaborationResult{}, ErrUnsupported
		}
		parts = append(parts, part.Text())
	}
	output := strings.Join(parts, "\n")
	if r.collaboration {
		if !collaborationEnabled(message) {
			return CollaborationResult{}, ErrInactive
		}
		metadata := new(cp.ResponseMetadata)
		if err := decodeCollaborationMetadata(message.Metadata[CollaborationURI], metadata); err != nil {
			return CollaborationResult{}, err
		}
		response := CollaborationResult{ResponseMetadata: metadata}
		if metadata.Status == cp.Status_STATUS_COMPLETED {
			if strings.TrimSpace(output) == "" {
				return CollaborationResult{}, ErrInvalidParams
			}
			response.Answer = output
		}
		return response, nil
	}
	if strings.TrimSpace(output) == "" {
		return CollaborationResult{}, errors.New("peer returned an empty answer")
	}
	return CollaborationResult{ResponseMetadata: &cp.ResponseMetadata{Status: cp.Status_STATUS_COMPLETED}, Answer: output}, nil
}
