package grant

import (
	"errors"
	"testing"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/proto"
)

func TestGrantRefPreservesEveryNativeScope(t *testing.T) {
	t.Parallel()
	source := &pb.Source{
		AgentId: "agent",
		TapeId:  "source",
	}
	secondStart := te.MustParseSeq("90071992547409931234567890")
	views := []*tv.EntryView{
		{
			Owner:     "owner",
			SessionId: "session",
			Scope: tv.EntryRange{
				SeqS: te.SeqFromUint64(2),
				SeqE: te.SeqFromUint64(4),
			},
		},
		{
			Owner:     "owner",
			SessionId: "session",
			Scope: tv.EntryRange{
				SeqS: secondStart,
				SeqE: secondStart.Next(),
			},
		},
	}
	ref, err := viewRef(source, "owner", views)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(ref.Source, source) || len(ref.Scopes) != 2 || ref.Scopes[0].SeqS != "2" || ref.Scopes[0].SeqE != "4" || ref.Scopes[1].SeqS != secondStart.String() || ref.Scopes[1].SeqE != secondStart.Next().String() {
		t.Fatalf("native view collection changed its source, gaps or sequence precision: %v", ref)
	}
	views[1].SessionId = "another-session"
	if _, err := viewRef(source, "owner", views); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("cross-session grant returned %v", err)
	}
	views[1].SessionId = "session"
	views[1].Owner = "other-owner"
	if _, err := viewRef(source, "owner", views); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("cross-owner grant returned %v", err)
	}
	views[1] = nil
	if _, err := viewRef(source, "owner", views); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("nil view returned %v", err)
	}
	if _, err := viewRef(source, "owner", nil); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("empty view collection returned %v", err)
	}
}
