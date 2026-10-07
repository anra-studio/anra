package a2a

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/proto"
)

func TestProtobufPreservesUnboundedSequencesAndJSON(t *testing.T) {
	seq := te.MustParseSeq("184467440737095516160000000001")
	now := time.Date(2026, 10, 7, 1, 2, 3, 456, time.UTC)
	d := Decision{RequestID: "request", Status: Granted, View: &View{ID: "view", Source: Source{AgentID: "peer", TapeID: "tape"}, SnapshotHead: seq, Ranges: []tv.EntryRange{{SeqS: seq, SeqE: seq.Next()}}, ExpiresAt: now}}
	wire, err := proto.Marshal(decisionToProto(d))
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.Decision
	if err := proto.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	again, err := decisionFromProto(&decoded, d.RequestID)
	if err != nil || again.View.SnapshotHead != seq || again.View.Ranges[0] != d.View.Ranges[0] || !again.View.ExpiresAt.Equal(now) {
		t.Fatalf("decision = %#v, %v", again, err)
	}
	payload := json.RawMessage(`{"integer":184467440737095516160000000001,"decimal":1.2300}`)
	page := Page{ViewID: "view", Source: d.View.Source, SnapshotHead: seq, Entries: []ExportedEntry{{ProjectedEntry: tv.ProjectedEntry{Seq: seq, Kind: te.EntryUser, Summary: "finding"}, Timestamp: now, Payload: &Payload{MediaType: TextMediaType, Data: payload}}}}
	wire, err = proto.Marshal(pageToProto(page))
	if err != nil {
		t.Fatal(err)
	}
	var decodedPage pb.Page
	if err := proto.Unmarshal(wire, &decodedPage); err != nil {
		t.Fatal(err)
	}
	read, err := pageFromProto(&decodedPage)
	if err != nil || read.Entries[0].Seq != seq || !bytes.Equal(read.Entries[0].Payload.Data, payload) {
		t.Fatalf("page = %#v, %v", read, err)
	}
	decoded.View.SnapshotHead = "01"
	if _, err := decisionFromProto(&decoded, d.RequestID); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("noncanonical sequence = %v", err)
	}
	decoded.View.SnapshotHead = seq.String()
	decoded.View.ExpiresAt.Seconds = 1 << 62
	if _, err := decisionFromProto(&decoded, d.RequestID); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("invalid timestamp = %v", err)
	}
	decodedPage.Entries[0].Payload.JsonData = []byte("invalid JSON")
	if _, err := pageFromProto(&decodedPage); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("invalid payload = %v", err)
	}
	for _, status := range []Status{Denied, Pending} {
		decision, err := decisionFromProto(decisionToProto(Decision{RequestID: "request", Status: status}), "request")
		if err != nil || decision.Status != status || decision.View != nil {
			t.Fatalf("status = %#v, %v", decision, err)
		}
	}
}
