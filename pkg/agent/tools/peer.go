package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	cp "github.com/anra-studio/anra/gen/anra/a2a/collaboration/v1"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	rv "github.com/anra-studio/anra/gen/anra/registry/v1"
	"github.com/anra-studio/anra/pkg/a2a"
	"github.com/anra-studio/anra/pkg/discovery"
	"github.com/anra-studio/anra/pkg/flow/clbr"
	"github.com/anra-studio/anra/pkg/model/delegator"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// Delegation hints use the public ProtoJSON spelling, not Go protobuf field tags.
type viewSource struct {
	AgentID string `json:"agentId"`

	TapeID string `json:"tapeId"`
}
type viewScope struct {
	SeqS string `json:"seqS"`
	SeqE string `json:"seqE"`
}
type viewRef struct {
	Source viewSource `json:"source"`

	Scopes []viewScope `json:"scopes"`
}

func (r *viewRef) message() *pb.Ref {
	if r == nil {
		return nil
	}

	out := &pb.Ref{
		Source: &pb.Source{
			AgentId: r.Source.AgentID,
			TapeId:  r.Source.TapeID,
		},
	}

	for _, scope := range r.Scopes {
		out.Scopes = append(
			out.Scopes,
			&pb.EntryRange{
				SeqS: scope.SeqS,
				SeqE: scope.SeqE,
			},
		)
	}

	return out
}

type (
	discoverArgs   struct{}
	discoverResult struct {
		Agents []map[string]any `json:"agents"`

		Context []map[string]any `json:"context,omitempty"`
	}
)

type sendArgs struct {
	Peer string `json:"peer" jsonschema:"Name of the registered destination agent."`

	Message string `json:"message" jsonschema:"A specific question for the destination agent."`

	Context []*viewRef `json:"context,omitempty" jsonschema:"Optional view references; each lender independently authorizes borrowing."`
}
type sendResult struct {
	Peer string `json:"peer"`

	Answer string `json:"answer,omitempty"`

	Status string `json:"status"`

	Retry string `json:"retry,omitempty"`

	View map[string]any `json:"view,omitempty"`
}

// NewPeerTools creates native ADK tools backed by the team's Peer directory and
// official A2A SDK. Tool calls and responses are persisted by the ADK runtime.
func NewPeerTools(directory *discovery.Client, h *http.Client, self string, collaboration *clbr.Service) (map[string]tool.Tool, error) {
	discover, err := functiontool.New(
		functiontool.Config{
			Name:        "discover_peers",
			Description: "Discover online peer agents, their identities, addresses and skills, and any view references supplied with this question. References must be borrowed under your own identity.",
		},
		func(ctx tool.Context, _ discoverArgs) (discoverResult, error) {
			if directory == nil {
				return discoverResult{}, errors.New("peer discovery requires a configured registry")
			}

			records, err := directory.List(
				ctx,
				&rv.ListRequest{
					State: rv.PresenceState_PRESENCE_STATE_ONLINE.Enum(),
				},
			)
			if err != nil {
				return discoverResult{}, err
			}

			result := discoverResult{
				Agents: make([]map[string]any, 0, len(records)),
			}

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
		},
	)
	if err != nil {
		return nil, err
	}

	send, err := functiontool.New(
		functiontool.Config{
			Name:        "send_peer_message",
			Description: "Ask a specific question of a discovered peer. Inspect status: completed has an answer; busy was not accepted and may identify borrowable context; no_new_context suppresses repeated BUSY. Continue reasoning without polling.",
		},
		func(ctx tool.Context, args sendArgs) (sendResult, error) {
			if directory == nil || h == nil {
				return sendResult{}, errors.New("peer messaging requires a configured registry")
			}

			refs := make([]*pb.Ref, len(args.Context))
			for i, ref := range args.Context {
				refs[i] = ref.message()
			}
			result, err := collaboration.Send(
				ctx,
				clbr.Request{
					Self:    self,
					Peer:    args.Peer,
					Message: args.Message,
					Context: refs,
				},
				func(outgoing context.Context, peer string) (clbr.Sender, error) {
					record, err := directory.Lookup(outgoing, peer)
					if err != nil {
						return nil, err
					}
					if err := discovery.RequireOnline(record); err != nil {
						return nil, err
					}
					return a2a.NewMessageClient(outgoing, h, record.Peer)
				},
			)
			if errors.Is(err, clbr.ErrInvalidParams) {
				return sendResult{}, fmt.Errorf("%w: %w", a2a.ErrInvalidParams, err)
			}
			if err != nil {
				return sendResult{}, err
			}
			if result.Suppressed {
				return sendResult{
					Peer:   args.Peer,
					Status: "no_new_context",
				}, nil
			}

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

			return sendResult{
				Peer:   args.Peer,
				Answer: result.Answer,
				Status: status,
				Retry:  retry,
				View:   view,
			}, nil
		},
	)
	if err != nil {
		return nil, err
	}
	return map[string]tool.Tool{"discover_peers": discover, "send_peer_message": send}, nil
}
