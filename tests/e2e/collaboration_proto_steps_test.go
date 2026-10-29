package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http/httptest"
	"strings"
	"time"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	cp "github.com/scbizu/anra/gen/anra/a2a/collaboration/v1"
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"github.com/scbizu/anra/pkg/a2a"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func delegationFixture() *cp.DelegationMetadata {
	return &cp.DelegationMetadata{RootRequestId: "protobuf-root", Deadline: timestamppb.New(time.Now().Add(5 * time.Second)), Ancestors: []string{"bob"}}
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
	client, err := a2aclient.NewFromCard(c.s.ctx, c.nodes["alice"].card, a2aclient.WithJSONRPCTransport(a2a.TrustedHTTPClient("bob")))
	if err != nil {
		return err
	}
	delegation := delegationFixture()
	delegation.Context = []*pb.TapeView{c.originalRef}
	metadata, err := metadataObject(delegation)
	if err != nil {
		return err
	}
	for _, expected := range []cp.Status{cp.Status_STATUS_COMPLETED, cp.Status_STATUS_BUSY} {
		var view *pb.TapeView
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
		event, err := client.SendMessage(c.s.ctx, &sdk.SendMessageRequest{Message: message})
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

func (c *collaborationScenario) rejectRequestMetadata(problem string) error {
	metadata, err := metadataObject(delegationFixture())
	if err != nil {
		return err
	}
	ref := map[string]any{"source": map[string]any{"agentId": "alice", "tapeId": "source"}, "snapshotHead": "3", "scope": map[string]any{"seqS": "1", "seqE": "3"}}
	switch problem {
	case "missing root":
		delete(metadata, "rootRequestId")
	case "oversized root":
		metadata["rootRequestId"] = strings.Repeat("x", 257)
	case "missing deadline":
		delete(metadata, "deadline")
	case "invalid deadline":
		metadata["deadline"] = "not-a-timestamp"
	case "empty ancestor":
		metadata["ancestors"] = []string{""}
	case "oversized ancestor":
		metadata["ancestors"] = []string{strings.Repeat("x", 129)}
	case "excessive context":
		metadata["context"] = []any{ref, ref, ref, ref, ref, ref, ref, ref, ref}
	case "malformed context":
		ref["snapshotHead"] = "01"
		metadata["context"] = []any{ref}
	case "missing context scope":
		delete(ref, "scope")
		metadata["context"] = []any{ref}
	case "reversed context scope":
		ref["scope"] = map[string]any{"seqS": "3", "seqE": "1"}
		metadata["context"] = []any{ref}
	case "uncommitted context boundary":
		ref["snapshotHead"] = "4"
		metadata["context"] = []any{ref}
	case "unknown field":
		metadata["caller"] = "body-controlled-identity"
	default:
		return fmt.Errorf("unknown request problem %q", problem)
	}
	n := c.nodes["alice"]
	before, err := c.readEntries(n)
	if err != nil {
		return err
	}
	client, err := a2aclient.NewFromCard(c.s.ctx, n.card, a2aclient.WithJSONRPCTransport(a2a.TrustedHTTPClient("bob")))
	if err != nil {
		return err
	}
	message := sdk.NewMessage(sdk.MessageRoleUser, sdk.NewTextPart("must be rejected"))
	message.Extensions = []string{a2a.CollaborationURI}
	message.Metadata = map[string]any{a2a.CollaborationURI: metadata}
	_, err = client.SendMessage(c.s.ctx, &sdk.SendMessageRequest{Message: message})
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

func (c *collaborationScenario) rejectResponseMetadata(problem string) error {
	metadata := map[string]any{"status": cp.Status_STATUS_COMPLETED.String()}
	answer := "completed answer"
	ref := map[string]any{"source": map[string]any{"agentId": "peer", "tapeId": "source"}, "snapshotHead": "3", "scope": map[string]any{"seqS": "1", "seqE": "3"}}
	switch problem {
	case "unspecified status":
		delete(metadata, "status")
	case "unknown status":
		metadata["status"] = 99
	case "unknown retry":
		metadata["status"] = cp.Status_STATUS_BUSY.String()
		metadata["retry"] = 99
	case "completed retry hint":
		metadata["retry"] = cp.RetryHint_RETRY_HINT_AFTER_NEW_VIEW.String()
	case "completed view":
		metadata["view"] = ref
	case "malformed busy view":
		metadata["status"] = cp.Status_STATUS_BUSY.String()
		ref["snapshotHead"] = "01"
		metadata["view"] = ref
	case "missing busy scope":
		metadata["status"] = cp.Status_STATUS_BUSY.String()
		delete(ref, "scope")
		metadata["view"] = ref
	case "uncommitted busy boundary":
		metadata["status"] = cp.Status_STATUS_BUSY.String()
		ref["snapshotHead"] = "4"
		metadata["view"] = ref
	case "answer in metadata":
		metadata["answer"] = "answer belongs in A2A parts"
	case "empty completed answer":
		answer = " "
	case "local suppression status":
		metadata["status"] = "no_new_context"
	case "legacy lowercase status":
		metadata["status"] = "busy"
	default:
		return fmt.Errorf("unknown response problem %q", problem)
	}
	server := httptest.NewUnstartedServer(nil)
	defer server.Close()
	base := "http://" + server.Listener.Addr().String()
	card := &sdk.AgentCard{Name: "peer", Version: "1", SupportedInterfaces: []*sdk.AgentInterface{sdk.NewAgentInterface(base+a2a.CorePath, sdk.TransportProtocolJSONRPC)}, DefaultInputModes: []string{"text/plain"}, DefaultOutputModes: []string{"text/plain"}}
	a2a.AdvertiseCollaboration(card)
	executor := a2asrv.AgentExecutorFunc(func(_ context.Context, _ *a2asrv.ExecutorContext) iter.Seq2[sdk.Event, error] {
		return func(yield func(sdk.Event, error) bool) {
			reply := sdk.NewMessage(sdk.MessageRoleAgent, sdk.NewTextPart(answer))
			reply.Extensions = []string{a2a.CollaborationURI}
			reply.Metadata = map[string]any{a2a.CollaborationURI: metadata}
			yield(reply, nil)
		}
	})
	handler, err := a2a.NewMessageHandler(card, a2asrv.NewHandler(executor), a2a.TrustedPeerIdentity)
	if err != nil {
		return err
	}
	server.Config.Handler = handler
	server.Start()
	client, err := messageClient(c.s.ctx, a2a.TrustedHTTPClient("bob"), base)
	if err != nil {
		return err
	}
	_, err = client.Send(c.s.ctx, "check peer response")
	if !errors.Is(err, a2a.ErrInvalidParams) {
		return fmt.Errorf("invalid response accepted: %v", err)
	}
	return nil
}
