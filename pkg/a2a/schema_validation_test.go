package a2a

import (
	"testing"

	"buf.build/go/protovalidate"
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Call Protovalidate directly so these checks cannot depend on Go wrappers.
func TestSchemaRejectsMalformedMessages(t *testing.T) {
	source := &pb.Source{AgentId: "peer", TapeId: "tape"}
	view := &pb.View{ViewId: "view", Source: source, SnapshotHead: "1", ExpiresAt: timestamppb.Now()}
	cases := map[string]proto.Message{
		"empty source":             &pb.Source{},
		"missing source agent":     &pb.Source{TapeId: "tape"},
		"missing source tape":      &pb.Source{AgentId: "peer"},
		"empty view":               &pb.View{},
		"missing decision":         &pb.BorrowResponse{},
		"missing get decision":     &pb.GetResponse{},
		"missing page":             &pb.ReadResponse{},
		"unspecified status":       &pb.Decision{RequestId: "request"},
		"unknown status":           &pb.Decision{RequestId: "request", Status: 42},
		"missing granted view":     &pb.Decision{RequestId: "request", Status: Granted},
		"denied with view":         &pb.Decision{RequestId: "request", Status: Denied, View: view},
		"pending with view":        &pb.Decision{RequestId: "request", Status: Pending, View: view},
		"missing decision request": &pb.Decision{Status: Denied},
		"missing media type":       &pb.Payload{JsonData: []byte(`{}`)},
		"empty JSON bytes":         &pb.Payload{MediaType: "application/json"},
		"noncanonical entry seq":   &pb.ExportedEntry{Seq: "01", Timestamp: timestamppb.Now()},
		"missing entry timestamp":  &pb.ExportedEntry{Seq: "1"},
		"missing page source":      &pb.Page{ViewId: "view", SnapshotHead: "1"},
		"missing page id":          &pb.Page{Source: source, SnapshotHead: "1"},
		"invalid page snapshot":    &pb.Page{ViewId: "view", Source: source, SnapshotHead: "-1"},
		"invalid nested entry":     &pb.Page{ViewId: "view", Source: source, SnapshotHead: "1", Entries: []*pb.ExportedEntry{{Seq: "1"}}},
		"nil nested entry":         &pb.Page{ViewId: "view", Source: source, SnapshotHead: "1", Entries: []*pb.ExportedEntry{nil}},
	}
	for name, change := range map[string]func(*pb.View){
		"missing view id":       func(v *pb.View) { v.ViewId = "" },
		"missing view source":   func(v *pb.View) { v.Source = nil },
		"missing expiry":        func(v *pb.View) { v.ExpiresAt = nil },
		"noncanonical snapshot": func(v *pb.View) { v.SnapshotHead = "01" },
		"reversed range":        func(v *pb.View) { v.Ranges = []*pb.EntryRange{{SeqS: "2", SeqE: "1"}} },
		"multiple ranges":       func(v *pb.View) { v.Ranges = []*pb.EntryRange{{SeqS: "1", SeqE: "2"}, {SeqS: "2", SeqE: "3"}} },
		"nil range":             func(v *pb.View) { v.Ranges = []*pb.EntryRange{nil} },
	} {
		v := proto.Clone(view).(*pb.View)
		change(v)
		cases[name] = v
	}
	for name, message := range cases {
		t.Run(name, func(t *testing.T) {
			if err := protovalidate.Validate(message); err == nil {
				t.Fatal("schema accepted malformed message")
			}
		})
	}
}
