package a2a

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	cp "github.com/scbizu/anra/gen/anra/a2a/collaboration/v1"
	"github.com/scbizu/anra/pkg/discovery"
	"github.com/scbizu/anra/pkg/model/delegator"
)

type MessageClient struct {
	client        *a2aclient.Client
	Name          string
	collaboration bool
	Card          *sdk.AgentCard
}

// NewMessageClient builds a client from the card the directory registered for
// the peer. The record is the only discovery step: the card is not fetched
// again, so a client always talks to the card the peer registered.
func NewMessageClient(ctx context.Context, h *http.Client, record discovery.Record) (*MessageClient, error) {
	if h == nil || record.Card == nil {
		return nil, ErrInvalidParams
	}
	card := record.Card
	if card.Name != record.Name {
		return nil, errors.New("discovered card name mismatch")
	}
	// BaseURL checks the card URL and every advertised interface origin.
	base, err := discovery.BaseURL(record)
	if err != nil {
		return nil, err
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
	return &MessageClient{client: client, Name: record.Name, collaboration: advertisesCollaboration(card), Card: card}, nil
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
