package a2a

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"github.com/scbizu/anra/pkg/discovery"
	"github.com/scbizu/anra/pkg/model/delegator"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/protobuf/encoding/protojson"
)

type borrowPeerArgs struct {
	Peer      string `json:"peer"`
	RequestID string `json:"requestId"`
	Purpose   string `json:"purpose"`
	Query     string `json:"query"`
}

// NewPeerBorrowTool resolves the lender's current card and lets the lender select
// context. Every request uses this agent's own HTTP client identity.
func NewPeerBorrowTool(directory *discovery.Client, h *http.Client, self string) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{Name: "borrow_peer_context", Description: "Ask a named peer for context using a purpose and query. The lender selects an eligible immutable publication, even while busy, and grants access under your own identity. Keep all returned provenance."}, func(ctx tool.Context, args borrowPeerArgs) (map[string]any, error) {
		if directory == nil || h == nil || args.Peer == self {
			return nil, errors.New("peer borrowing requires a different discovered peer")
		}
		record, err := directory.Lookup(ctx, args.Peer)
		if err != nil {
			return nil, err
		}
		// The message client validates the registered card and its endpoints.
		remote, err := NewMessageClient(ctx, h, record)
		if err != nil {
			return nil, err
		}
		client, err := NewClient(h, remote.Card)
		if err != nil {
			return nil, err
		}
		req := &pb.BorrowRequest{RequestId: args.RequestID, Purpose: args.Purpose, Query: args.Query}
		encoded, err := protojson.Marshal(req)
		if err != nil {
			return nil, err
		}
		payload, err := (BorrowTool{Client: client}).Execute(ctx, encoded)
		if err != nil {
			return nil, err
		}
		var envelope borrowToolResult
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return nil, err
		}
		var decision pb.Decision
		if err := protojson.Unmarshal(envelope.Decision, &decision); err != nil {
			return nil, fmt.Errorf("borrow result has no valid decision: %w", err)
		}
		var result map[string]any
		if err := json.Unmarshal(payload, &result); err != nil {
			return nil, err
		}
		// A newly borrowed publication is evidence of progress, unlike a changed prompt.
		if decision.Status != Granted || decision.View == nil {
			return result, nil
		}
		delegator.FromContext(ctx).Borrowed(args.Peer, decision.View)
		return result, nil
	})
}
