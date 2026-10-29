package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	cp "github.com/scbizu/anra/gen/anra/a2a/collaboration/v1"
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	rv "github.com/scbizu/anra/gen/anra/registry/v1"
	"github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/discovery"
	"github.com/scbizu/anra/pkg/model/delegator"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// Delegation hints use the public ProtoJSON spelling, not Go protobuf field tags.
type viewSource struct {
	AgentID string `json:"agentId"`
	TapeID  string `json:"tapeId"`
}
type viewScope struct {
	SeqS string `json:"seqS"`
	SeqE string `json:"seqE"`
}
type viewRef struct {
	Source       viewSource `json:"source"`
	SnapshotHead string     `json:"snapshotHead"`
	Scope        viewScope  `json:"scope"`
}

func (r *viewRef) message() *pb.TapeView {
	if r == nil {
		return nil
	}
	return &pb.TapeView{Source: &pb.Source{AgentId: r.Source.AgentID, TapeId: r.Source.TapeID}, SnapshotHead: r.SnapshotHead, Scope: &pb.EntryRange{SeqS: r.Scope.SeqS, SeqE: r.Scope.SeqE}}
}

type discoverArgs struct{}
type discoverResult struct {
	Agents  []map[string]any `json:"agents"`
	Context []map[string]any `json:"context,omitempty"`
}
type sendArgs struct {
	Peer    string     `json:"peer" jsonschema:"Name of the registered destination agent."`
	Message string     `json:"message" jsonschema:"A specific question for the destination agent."`
	Context []*viewRef `json:"context,omitempty" jsonschema:"Optional view references; each lender independently authorizes borrowing."`
}
type sendResult struct {
	Peer   string         `json:"peer"`
	Answer string         `json:"answer,omitempty"`
	Status string         `json:"status"`
	Retry  string         `json:"retry,omitempty"`
	View   map[string]any `json:"view,omitempty"`
}

// NewPeerTools creates native ADK tools backed by the team's Peer directory and
// official A2A SDK. Tool calls and responses are persisted by the ADK runtime.
func NewPeerTools(directory *discovery.Client, h *http.Client, self string) (map[string]tool.Tool, error) {
	discover, err := functiontool.New(functiontool.Config{Name: "discover_peers", Description: "Discover online peer agents, their identities, addresses and skills, and any view references supplied with this question. References must be borrowed under your own identity."}, func(ctx tool.Context, _ discoverArgs) (discoverResult, error) {
		if directory == nil {
			return discoverResult{}, errors.New("peer discovery requires a configured registry")
		}
		records, err := directory.List(ctx, &rv.ListRequest{State: rv.PresenceState_PRESENCE_STATE_ONLINE.Enum()})
		if err != nil {
			return discoverResult{}, err
		}
		result := discoverResult{Agents: make([]map[string]any, 0, len(records))}
		for _, record := range records {
			if record.Peer.Id != self {
				// Expose the canonical protobuf JSON shape to the model.
				value, err := messageObject(record.Peer)
				if err != nil {
					return discoverResult{}, err
				}
				result.Agents = append(result.Agents, value)
			}
		}
		for _, ref := range delegator.FromContext(ctx).Metadata().Context {
			value, err := messageObject(ref)
			if err != nil {
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
		refs := make([]*pb.TapeView, len(args.Context))
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
			return sendResult{}, fmt.Errorf("%w: %w", a2a.ErrInvalidParams, err)
		}
		record, err := directory.Lookup(outgoing, args.Peer)
		if err != nil {
			return sendResult{}, err
		}
		if err := discovery.RequireOnline(record); err != nil {
			return sendResult{}, err
		}
		client, err := a2a.NewMessageClient(ctx, h, record.Peer)
		if err != nil {
			return sendResult{}, err
		}
		result, err := client.Send(outgoing, args.Message)
		if err != nil {
			return sendResult{}, err
		}
		d.Observe(args.Peer, result.ResponseMetadata)
		var view map[string]any
		if result.View != nil {
			view, err = messageObject(result.View)
			if err != nil {
				return sendResult{}, err
			}
		}
		// Tool statuses are readable labels; the wire uses protobuf enum names.
		status := strings.ToLower(strings.TrimPrefix(result.Status.String(), "STATUS_"))
		var retry string
		if result.Retry != cp.RetryHint_RETRY_HINT_UNSPECIFIED {
			retry = strings.ToLower(strings.TrimPrefix(result.Retry.String(), "RETRY_HINT_"))
		}
		return sendResult{Peer: args.Peer, Answer: result.Answer, Status: status, Retry: retry, View: view}, nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]tool.Tool{"discover_peers": discover, "send_peer_message": send}, nil
}
