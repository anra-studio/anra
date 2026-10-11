package a2a

import (
	"bytes"
	"encoding/json"
	"errors"
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"testing"
	"time"
)

func TestProtobufPreservesUnboundedSequencesAndJSON(t *testing.T) {
	seq := te.MustParseSeq("184467440737095516160000000001")
	now := time.Date(2026, 10, 7, 1, 2, 3, 456, time.UTC)
	d := &pb.Decision{RequestId: "request", Status: Granted, View: &pb.View{ViewId: "view", Source: &pb.Source{AgentId: "peer", TapeId: "tape"}, SnapshotHead: seq.String(), Ranges: []*pb.EntryRange{{SeqS: seq.String(), SeqE: seq.Next().String()}}, ExpiresAt: timestamppb.New(now)}}
	wire, err := proto.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.Decision
	if err := proto.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := validateDecision(&decoded, d.RequestId); err != nil || !proto.Equal(d, &decoded) {
		t.Fatalf("decision=%v, error=%v", &decoded, err)
	}
	payload := json.RawMessage(`{"integer":184467440737095516160000000001,"decimal":1.2300}`)
	page := &pb.Page{ViewId: "view", Source: d.View.Source, SnapshotHead: seq.String(), Entries: []*pb.ExportedEntry{{Seq: seq.String(), Kind: string(te.EntryUser), Summary: "finding", Timestamp: timestamppb.New(now), Payload: &pb.Payload{MediaType: TextMediaType, JsonData: payload}}}}
	wire, err = proto.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	var decodedPage pb.Page
	if err := proto.Unmarshal(wire, &decodedPage); err != nil {
		t.Fatal(err)
	}
	if err := validatePage(&decodedPage); err != nil || !bytes.Equal(decodedPage.Entries[0].Payload.JsonData, payload) {
		t.Fatalf("page=%v, error=%v", &decodedPage, err)
	}
	decoded.View.SnapshotHead = "01"
	if err := validateDecision(&decoded, d.RequestId); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("noncanonical sequence=%v", err)
	}
	decoded.View.SnapshotHead = seq.String()
	decoded.View.ExpiresAt.Seconds = 1 << 62
	if err := validateDecision(&decoded, d.RequestId); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("invalid timestamp=%v", err)
	}
	decodedPage.Entries[0].Payload.JsonData = []byte("invalid JSON")
	if err := validatePage(&decodedPage); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("invalid payload=%v", err)
	}
	for _, status := range []pb.DecisionStatus{Denied, Pending} {
		if err := validateDecision(&pb.Decision{RequestId: "request", Status: status}, "request"); err != nil {
			t.Fatal(err)
		}
	}
}
