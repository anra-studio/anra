package a2a

import (
	"context"
	"encoding/json"
	"slices"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	cp "github.com/scbizu/anra/gen/anra/a2a/collaboration/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const CollaborationURI = "urn:anra:a2a:collaboration:v1"
const maxCollaborationMetadataBytes = 8192

// CollaborationResult combines generated response metadata with answer text
// extracted from standard A2A message parts. It is not a wire message.
type CollaborationResult struct {
	*cp.ResponseMetadata
	// Answer is the text of the A2A message parts, not a metadata field.
	Answer string
}

// Requester is the transport-authenticated identity, never a message body field.
func Requester(ctx context.Context) string { p, _ := ctx.Value(peerKey{}).(string); return p }

// A2A metadata remains a JSON object. ProtoJSON and the generated descriptors
// define its field names, enum spelling, timestamps, and structural validation.
func encodeCollaborationMetadata(message proto.Message) (map[string]any, error) {
	if err := validateMessage(message); err != nil {
		return nil, err
	}
	data, err := protojson.Marshal(message)
	if err != nil || len(data) > maxCollaborationMetadataBytes {
		return nil, ErrInvalidParams
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, ErrInvalidParams
	}
	return value, nil
}

func decodeCollaborationMetadata(value any, message proto.Message) error {
	data, err := json.Marshal(value)
	if err != nil || len(data) > maxCollaborationMetadataBytes {
		return ErrInvalidParams
	}
	if err := protojson.Unmarshal(data, message); err != nil {
		return ErrInvalidParams
	}
	return validateMessage(message)
}

func advertisesCollaboration(card *sdk.AgentCard) bool {
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == CollaborationURI {
			return true
		}
	}
	return false
}
func AdvertiseCollaboration(card *sdk.AgentCard) {
	if !advertisesCollaboration(card) {
		card.Capabilities.Extensions = append(card.Capabilities.Extensions, sdk.AgentExtension{URI: CollaborationURI, Description: "Nonblocking reasoning admission and bounded peer collaboration"})
	}
}
func collaborationEnabled(m *sdk.Message) bool {
	return slices.Contains(m.Extensions, CollaborationURI)
}
