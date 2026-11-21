package e2e_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	cp "github.com/anra-studio/anra/gen/anra/a2a/collaboration/v1"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/anra-studio/anra/pkg/a2a"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func delegationFixture() *cp.DelegationMetadata {
	return &cp.DelegationMetadata{
		RootRequestId: "protobuf-root",
		Deadline:      timestamppb.New(time.Now().Add(5 * time.Second)),
		Ancestors:     []string{"bob"},
	}
}

func metadataObject(message proto.Message) (map[string]any, error) {
	data, err := protojson.Marshal(message)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	err = json.Unmarshal(data, &object)
	return object, err
}

func (c *collaborationScenario) protobufRoundtrip() error {
	client, err := c.a2aClient("bob", "alice")
	if err != nil {
		return err
	}
	delegation := delegationFixture()

	delegation.Context = []*pb.Ref{c.originalRef}

	metadata, err := metadataObject(delegation)
	if err != nil {
		return err
	}
	for _, expected := range []cp.Status{cp.Status_STATUS_COMPLETED, cp.Status_STATUS_BUSY} {

		var view *pb.Ref

		if expected == cp.Status_STATUS_BUSY {
			view, err = c.nodes["alice"].a.View(c.s.ctx, "bob", nil)
			if err != nil {
				return err
			}
			if err := c.hold(); err != nil {
				return err
			}
		}
		message := sdk.NewMessage(sdk.MessageRoleUser, sdk.NewTextPart("protobuf roundtrip"))
		message.Extensions = []string{a2a.CollaborationURI}
		message.Metadata = map[string]any{a2a.CollaborationURI: metadata}

		event, err := client.SendMessage(
			c.s.ctx,
			&sdk.SendMessageRequest{
				Message: message,
			},
		)
		if err != nil {
			return err
		}
		reply, ok := event.(*sdk.Message)
		if !ok || len(reply.Parts) != 1 {
			return fmt.Errorf("not a direct A2A response: %+v", event)
		}
		wire, err := json.Marshal(reply.Metadata[a2a.CollaborationURI])
		if err != nil {
			return err
		}
		var response cp.ResponseMetadata
		if err := protojson.Unmarshal(wire, &response); err != nil {
			return err
		}
		var fields map[string]any
		if err := json.Unmarshal(wire, &fields); err != nil {
			return err
		}
		if response.Status != expected || fields["status"] != expected.String() || fields["answer"] != nil {
			return fmt.Errorf("response is not generated ProtoJSON: %s", wire)
		}
		if expected == cp.Status_STATUS_COMPLETED {
			if reply.Parts[0].Text() != "answer: protobuf roundtrip" || response.View != nil || response.Retry != cp.RetryHint_RETRY_HINT_UNSPECIFIED {
				return fmt.Errorf("invalid completed response: %s", wire)
			}
		} else if response.Retry != cp.RetryHint_RETRY_HINT_AFTER_NEW_VIEW || !proto.Equal(response.View, view) || !strings.Contains(reply.Parts[0].Text(), "request was not accepted") {
			return fmt.Errorf("invalid busy response: %s", wire)
		}
	}
	return c.finishHold()
}

func (c *collaborationScenario) rejectRequestMetadata() error {
	metadata, err := metadataObject(delegationFixture())
	if err != nil {
		return err
	}
	delete(metadata, "rootRequestId")
	n := c.nodes["alice"]
	before, err := c.readEntries(n)
	if err != nil {
		return err
	}
	client, err := c.a2aClient("bob", "alice")
	if err != nil {
		return err
	}
	message := sdk.NewMessage(sdk.MessageRoleUser, sdk.NewTextPart("must be rejected"))
	message.Extensions = []string{a2a.CollaborationURI}
	message.Metadata = map[string]any{a2a.CollaborationURI: metadata}

	_, err = client.SendMessage(
		c.s.ctx,
		&sdk.SendMessageRequest{
			Message: message,
		},
	)

	if !errors.Is(err, sdk.ErrInvalidParams) {
		return fmt.Errorf("invalid delegation did not return invalid params: %v", err)
	}
	after, err := c.readEntries(n)
	if err != nil {
		return err
	}
	if len(before) != len(after) || n.calls.Load() != 0 {
		return errors.New("invalid delegation entered reasoning or changed tape")
	}
	return nil
}

func (c *collaborationScenario) rejectCallerIdentity() error {
	node := c.nodes["alice"]
	before, err := c.readEntries(node)
	if err != nil {
		return err
	}
	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "message/send",
		"params": &sdk.SendMessageRequest{
			Message: sdk.NewMessage(sdk.MessageRoleUser, sdk.NewTextPart("must be rejected")),
		},
	})
	if err != nil {
		return err
	}
	for _, id := range []string{"", "bad/name"} {
		client := restClient{
			ctx:      c.s.ctx,
			base:     node.server.URL,
			identity: id,
		}
		status, _, err := client.call(http.MethodPost, a2a.CorePath, request)
		if err != nil {
			return err
		}
		if status != http.StatusUnauthorized {
			return fmt.Errorf("A2A caller %q returned HTTP %d, want 401", id, status)
		}
	}
	after, err := c.readEntries(node)
	if err != nil {
		return err
	}
	if len(before) != len(after) || node.calls.Load() != 0 {
		return errors.New("invalid caller entered reasoning or changed tape")
	}
	return nil
}

func (c *collaborationScenario) streamAnswer() error {
	ctx := c.s.ctx
	httpClient := a2a.TrustedHTTPClient("bob")
	card, err := a2a.Discover(ctx, httpClient, c.nodes["alice"].server.URL+a2asrv.WellKnownAgentCardPath)
	if err != nil {
		return err
	}
	client, err := newA2AClient(ctx, httpClient, card)
	if err != nil {
		return err
	}
	const input = "stream this answer"
	var output strings.Builder
	for event, err := range client.SendStreamingMessage(
		ctx,
		&sdk.SendMessageRequest{
			Message: sdk.NewMessage(sdk.MessageRoleUser, sdk.NewTextPart(input)),
		},
	) {
		if err != nil {
			return err
		}
		if message, ok := event.(*sdk.Message); ok {
			for _, part := range message.Parts {
				output.WriteString(part.Text())
			}
		}
	}
	if output.String() != "answer: "+input {
		return fmt.Errorf("streamed output = %q", output.String())
	}
	return nil
}
