package view

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestQuoteCodecsPreserveScopesAndOpaqueNumbers(t *testing.T) {
	content := json.RawMessage(`{"answer":18446744073709551616,"fraction":1e-1000,"empty":{}}`)
	ref := &pb.Ref{
		Source: &pb.Source{
			AgentId: "source",
			TapeId:  "incarnation",
		},
		Scopes: []*pb.EntryRange{{
			SeqS: "3",
			SeqE: "5",
		}, {
			SeqS: "18446744073709551616",
			SeqE: "18446744073709551618",
		}},
	}
	if err := protovalidate.Validate(ref); err != nil {
		t.Fatal(err)
	}
	original := &Quote{
		Channel: Borrowed,
		Provenance: Provenance{
			Caller:  "reader",
			LeaseID: "lease",
			Ref:     ref,
		},
		Entries: []Entry{{
			Seq:       "3",
			Kind:      "tool_result",
			Timestamp: time.Unix(100, 0).UTC(),
			MediaType: ContextMediaType,
			Content:   content,
		}},
	}
	wire, err := protojson.Marshal(original.Proto())
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.Quote
	if err := protojson.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(FromProto(&decoded))
	if err != nil {
		t.Fatal(err)
	}
	var result Quote
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(result.Provenance.Ref, ref) || !bytes.Equal(result.Entries[0].Content, content) {
		t.Fatalf("quote codecs changed scopes or number lexemes: %s", encoded)
	}
}

func TestRefRequiresNonemptyCanonicalDecimalScopes(t *testing.T) {
	valid := &pb.Ref{
		Source: &pb.Source{
			AgentId: "source",
			TapeId:  "incarnation",
		},
		Scopes: []*pb.EntryRange{{
			SeqS: "18446744073709551616",
			SeqE: "18446744073709551618",
		}},
	}
	for _, problem := range []string{"empty", "zero", "leading zero", "reversed"} {
		ref := proto.Clone(valid).(*pb.Ref)
		switch problem {
		case "empty":
			ref.Scopes = nil
		case "zero":
			ref.Scopes[0].SeqS = "0"
		case "leading zero":
			ref.Scopes[0].SeqS = "01"
		case "reversed":
			ref.Scopes[0].SeqE = ref.Scopes[0].SeqS
		}
		if err := protovalidate.Validate(ref); err == nil {
			t.Errorf("accepted %s scopes", problem)
		}
	}
}
