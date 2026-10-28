package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	cp "github.com/scbizu/anra/gen/anra/a2a/collaboration/v1"
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"github.com/scbizu/anra/pkg/discovery"
	"github.com/scbizu/anra/pkg/model/anchor"
	"github.com/scbizu/anra/pkg/model/delegator"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// Delegation hints use the public ProtoJSON spelling, not Go protobuf field tags.
type publicationSource struct {
	AgentID string `json:"agentId"`
	TapeID  string `json:"tapeId"`
}
type publicationRef struct {
	Source publicationSource `json:"source"`
	Seq    string            `json:"seq"`
}

func (r *publicationRef) message() *pb.AnchorRef {
	if r == nil {
		return nil
	}
	return &pb.AnchorRef{Source: &pb.Source{AgentId: r.Source.AgentID, TapeId: r.Source.TapeID}, Seq: r.Seq}
}

type discoverArgs struct{}
type discoverResult struct {
	Agents  []map[string]any `json:"agents"`
	Context []map[string]any `json:"context,omitempty"`
}
type sendArgs struct {
	Peer    string            `json:"peer" jsonschema:"Name of the registered destination agent."`
	Message string            `json:"message" jsonschema:"A specific question for the destination agent."`
	Context []*publicationRef `json:"context,omitempty" jsonschema:"Optional publication references; each lender independently authorizes borrowing."`
}
type sendResult struct {
	Peer        string         `json:"peer"`
	Answer      string         `json:"answer,omitempty"`
	Status      string         `json:"status"`
	Retry       string         `json:"retry,omitempty"`
	Publication map[string]any `json:"publication,omitempty"`
}

// PeerTools creates native ADK tools backed by the team's leased directory and
// official A2A SDK. Tool calls and responses are persisted by the ADK runtime.
func PeerTools(directory *discovery.Client, h *http.Client, self string) (map[string]tool.Tool, error) {
	discover, err := functiontool.New(functiontool.Config{Name: "discover_peers", Description: "Discover available peer agents, their Agent Cards and skills, and any publication references supplied with this question. References must be borrowed under your own identity."}, func(ctx tool.Context, _ discoverArgs) (discoverResult, error) {
		if directory == nil {
			return discoverResult{}, errors.New("peer discovery requires a configured registry")
		}
		records, err := directory.List(ctx)
		if err != nil {
			return discoverResult{}, err
		}
		result := discoverResult{Agents: make([]map[string]any, 0, len(records))}
		for _, record := range records {
			if record.Name != self {
				// SDK cards contain custom JSON union types. Expose their wire
				// representation so ADK validates the actual JSON shape.
				encoded, err := json.Marshal(record)
				if err != nil {
					return discoverResult{}, err
				}
				var value map[string]any
				if err := json.Unmarshal(encoded, &value); err != nil {
					return discoverResult{}, err
				}
				result.Agents = append(result.Agents, value)
			}
		}
		for _, ref := range delegator.FromContext(ctx).Metadata().Context {
			var value map[string]any
			if err := json.Unmarshal(anchor.ProtoJSON(ref), &value); err != nil {
				return discoverResult{}, err
			}
			result.Context = append(result.Context, value)
		}
		return result, nil
	})
	if err != nil {
		return nil, err
	}
	send, err := functiontool.New(functiontool.Config{Name: "send_peer_message", Description: "Ask a specific question of a discovered peer. Inspect status: completed has an answer; busy was not accepted and may identify borrowable context; no_new_context suppresses repeated BUSY. Continue reasoning without polling."}, func(ctx tool.Context, args sendArgs) (sendResult, error) {
		if directory == nil || h == nil {
			return sendResult{}, errors.New("peer messaging requires a configured registry")
		}
		if args.Peer == self || strings.TrimSpace(args.Message) == "" {
			return sendResult{}, errors.New("a different peer and a non-empty message are required")
		}
		d := delegator.FromContext(ctx)
		refs := make([]*pb.AnchorRef, len(args.Context))
		for i, ref := range args.Context {
			refs[i] = ref.message()
		}
		outgoing, err := d.Next(ctx, self, args.Peer, refs)
		switch {
		case errors.Is(err, delegator.ErrNoNewContext):
			return sendResult{Peer: args.Peer, Status: "no_new_context"}, nil
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return sendResult{}, err
		case err != nil:
			return sendResult{}, fmt.Errorf("%w: %w", ErrInvalidParams, err)
		}
		record, err := directory.Lookup(outgoing, args.Peer)
		if err != nil {
			return sendResult{}, err
		}
		client, err := NewMessageClient(ctx, h, record)
		if err != nil {
			return sendResult{}, err
		}
		result, err := client.Send(outgoing, args.Message)
		if err != nil {
			return sendResult{}, err
		}
		d.Observe(args.Peer, result.ResponseMetadata)
		var publication map[string]any
		if result.Publication != nil {
			_ = json.Unmarshal(anchor.ProtoJSON(result.Publication), &publication)
		}
		// Tool statuses are readable labels; the wire uses protobuf enum names.
		status := strings.ToLower(strings.TrimPrefix(result.Status.String(), "STATUS_"))
		var retry string
		if result.Retry != cp.RetryHint_RETRY_HINT_UNSPECIFIED {
			retry = strings.ToLower(strings.TrimPrefix(result.Retry.String(), "RETRY_HINT_"))
		}
		return sendResult{Peer: args.Peer, Answer: result.Answer, Status: status, Retry: retry, Publication: publication}, nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]tool.Tool{"discover_peers": discover, "send_peer_message": send}, nil
}
