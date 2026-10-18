package journal

import (
	"encoding/json"
	"testing"
	"time"

	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestJournalRecordsRoundTripLargeSequences(t *testing.T) {
	req := &pb.BorrowRequest{RequestId: "request", Purpose: "research", Query: "storage", Bounds: &pb.EntryRange{SeqS: "18446744073709551616", SeqE: "18446744073709551617"}}
	view := &pb.View{ViewId: "view", Source: &pb.Source{AgentId: "peer", TapeId: "tape"}, SnapshotHead: "18446744073709551616", Ranges: []*pb.EntryRange{req.Bounds}, ExpiresAt: timestamppb.New(time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC))}
	request, err := storeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := storeView(view)
	if err != nil {
		t.Fatal(err)
	}
	rec := requestState{Key: key("borrower", "request"), RequestID: "request", Requester: "borrower", Kind: grantKind, Request: request, ViewID: "view", View: &stored}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var decoded requestState
	if err := decode(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(decoded.Request.message(), req) || !proto.Equal(decoded.View.message(decoded.ViewID), view) {
		t.Fatalf("grant changed: %s", data)
	}
	pending := requestState{Key: key("borrower", "request"), RequestID: "request", Requester: "borrower", Kind: requestKind, Request: request, Status: pendingStatus}
	data, err = json.Marshal(pending)
	if err != nil {
		t.Fatal(err)
	}
	if err := decode(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(decoded.Request.message(), req) || decoded.Status != pendingStatus {
		t.Fatalf("request record changed: %s", data)
	}
}
