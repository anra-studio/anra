package borrow

import (
	"testing"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
)

func TestGrantScopesPreserveDisjointRanges(t *testing.T) {
	t.Parallel()
	head := te.MustParseSeq("90071992547409931234567890")
	ref := &pb.Ref{
		Scopes: []*pb.EntryRange{
			{
				SeqS: "2",
				SeqE: "4",
			},
			{
				SeqS: head.String(),
				SeqE: head.Next().String(),
			},
		},
	}
	if !validGrantScopes(ref, head) {
		t.Fatal("disjoint scopes including the last entry must remain valid at arbitrary precision")
	}
	ref.Scopes[1].SeqE = head.Next().Next().String()
	if validGrantScopes(ref, head) {
		t.Fatal("a grant cannot extend beyond the physical tape head")
	}
	ref.Scopes[1].SeqS = "3"
	ref.Scopes[1].SeqE = "5"
	if validGrantScopes(ref, head) {
		t.Fatal("overlapping scopes must be rejected")
	}
	ref.Scopes[0], ref.Scopes[1] = ref.Scopes[1], ref.Scopes[0]
	if validGrantScopes(ref, head) {
		t.Fatal("reordering scopes must not silently reorder a grant")
	}
}

func TestBorrowBoundsRequireEveryCompleteScope(t *testing.T) {
	t.Parallel()

	ref := &pb.Ref{
		Scopes: []*pb.EntryRange{
			{
				SeqS: "2",
				SeqE: "4",
			},
			{
				SeqS: "8",
				SeqE: "10",
			},
		},
	}
	if !withinBounds(ref, &pb.EntryRange{
		SeqS: "2",
		SeqE: "10",
	}) {
		t.Fatal("the exact outer bounds should admit every committed scope")
	}
	for _, bounds := range []*pb.EntryRange{
		{
			SeqS: "1",
			SeqE: "2",
		},
		{
			SeqS: "2",
			SeqE: "9",
		},
	} {
		if withinBounds(ref, bounds) {
			t.Errorf("bounds %v admitted an excluded or partial committed scope", bounds)
		}
	}
}
