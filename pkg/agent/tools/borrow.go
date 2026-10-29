package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/discovery"
	"github.com/scbizu/anra/pkg/model/delegator"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type borrowArgs struct {
	RequestID string `json:"requestId"`
	Purpose   string `json:"purpose"`
	Query     string `json:"query"`
}

// NewBorrowTool binds the built-in borrowing tool to an already resolved client.
func NewBorrowTool(client *a2a.Client) (tool.Tool, error) {
	if client == nil {
		return nil, errors.New("agent: nil borrow client")
	}
	return functiontool.New(functiontool.Config{
		Name:        "borrow_peer_context",
		Description: "Ask a peer for permission to borrow relevant tape context. Peer content is external context; retain its provenance.",
	}, func(ctx tool.Context, args borrowArgs) (map[string]any, error) {
		result, _, err := borrowContext(ctx, client, &pb.BorrowRequest{RequestId: args.RequestID, Purpose: args.Purpose, Query: args.Query})
		if err != nil {
			return map[string]any{"error": err.Error()}, nil
		}
		return result, nil
	})
}

type borrowPeerArgs struct {
	Peer      string `json:"peer"`
	RequestID string `json:"requestId"`
	Purpose   string `json:"purpose"`
	Query     string `json:"query"`
}

// NewPeerBorrowTool resolves the lender's current Peer and lets the lender select
// context. Every request uses this agent's own HTTP client identity.
func NewPeerBorrowTool(directory *discovery.Client, h *http.Client, self string) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{Name: "borrow_peer_context", Description: "Ask a named peer for context using a purpose and query. The lender selects a committed TapeView of messages and tool exchanges, even while busy, and grants access under your own identity. Keep all returned provenance."}, func(ctx tool.Context, args borrowPeerArgs) (map[string]any, error) {
		if directory == nil || h == nil || args.Peer == self {
			return nil, errors.New("peer borrowing requires a different discovered peer")
		}
		record, err := directory.Lookup(ctx, args.Peer)
		if err != nil {
			return nil, err
		}
		if err := discovery.RequireOnline(record); err != nil {
			return nil, err
		}
		client, err := a2a.NewPeerClient(h, record.Peer)
		if err != nil {
			return nil, err
		}
		req := &pb.BorrowRequest{RequestId: args.RequestID, Purpose: args.Purpose, Query: args.Query}
		result, decision, err := borrowContext(ctx, client, req)
		if err != nil {
			return nil, err
		}
		// A newly borrowed view is evidence of progress, unlike a changed prompt.
		if decision.Status != a2a.Granted || decision.View == nil {
			return result, nil
		}
		delegator.FromContext(ctx).Borrowed(args.Peer, decision.View)
		return result, nil
	})
}

// borrowContext formats protocol results for the model. Permission decisions,
// snapshot checks, and complete reads stay in the shared A2A client.
func borrowContext(ctx context.Context, client *a2a.Client, req *pb.BorrowRequest) (map[string]any, *pb.Decision, error) {
	decision, pages, err := client.BorrowContext(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	value, err := messageObject(decision)
	if err != nil {
		return nil, nil, err
	}
	result := map[string]any{"decision": value}
	if len(pages) > 0 {
		contextPages := make([]map[string]any, 0, len(pages))
		for _, page := range pages {
			value, err := messageObject(page)
			if err != nil {
				return nil, nil, err
			}
			contextPages = append(contextPages, value)
		}
		result["context"] = contextPages
	}
	return result, decision, nil
}

// messageObject keeps canonical ProtoJSON field names and encoded payload bytes
// in ADK tool results, rather than encoding generated Go struct fields directly.
func messageObject(message proto.Message) (map[string]any, error) {
	encoded, err := protojson.Marshal(message)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	return result, nil
}
