package delegator

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	cp "github.com/anra-studio/anra/gen/anra/a2a/collaboration/v1"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestDelegationForksPreserveRootAndDeepAncestry(t *testing.T) {
	t.Parallel()

	root, cancel := Begin(t.Context())
	defer cancel()
	left, err := FromContext(root).Next(root, "alice", "bob", nil)
	if err != nil {
		t.Fatal(err)
	}
	right, err := FromContext(root).Next(root, "alice", "charlie", nil)
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{"alice"}
	for hop := range 9 {
		self := fmt.Sprintf("peer-%d", hop)
		left, err = FromContext(left).Next(left, self, "next", nil)
		if err != nil {
			t.Fatal(err)
		}
		expected = append(expected, self)
	}

	rootMetadata := FromContext(root).Metadata()
	leftMetadata := FromContext(left).Metadata()
	rightMetadata := FromContext(right).Metadata()
	if leftMetadata.RootRequestId != rootMetadata.RootRequestId || rightMetadata.RootRequestId != rootMetadata.RootRequestId ||
		!slices.Equal(leftMetadata.Ancestors, expected) || !slices.Equal(rightMetadata.Ancestors, []string{"alice"}) || len(rootMetadata.Ancestors) != 0 {
		t.Fatalf("unexpected paths: root=%v left=%v right=%v", rootMetadata, leftMetadata, rightMetadata)
	}
}

func TestBusyMemoryRequiresNewerBorrowedContextAndIsTurnLocal(t *testing.T) {
	t.Parallel()

	ctx, cancel := Begin(t.Context())
	defer cancel()
	turn := FromContext(ctx)
	previous := busyView("5")
	turn.Observe("alice", &cp.ResponseMetadata{
		Status: cp.Status_STATUS_BUSY,
		View:   previous,
	})
	for _, ref := range []*pb.Ref{nil, busyView("4"), previous} {
		if ref != nil {
			turn.Borrowed("alice", &pb.Lease{
				Ref: ref,
			})
		}
		if _, err := turn.Next(ctx, "bob", "alice", nil); !errors.Is(err, ErrNoNewContext) {
			t.Fatalf("unchanged context %v returned %v", ref, err)
		}
	}

	newTurn, finish := Begin(ctx)
	defer finish()
	if _, err := FromContext(newTurn).Next(newTurn, "bob", "alice", nil); err != nil {
		t.Fatalf("BUSY state leaked across turns: %v", err)
	}
	turn.Borrowed("alice", &pb.Lease{
		Ref: busyView("6"),
	})
	if _, err := turn.Next(ctx, "bob", "alice", nil); err != nil {
		t.Fatalf("newer context failed to permit delegation: %v", err)
	}
}

func TestExpiredDelegationCannotOpenAnEdge(t *testing.T) {
	t.Parallel()
	ctx, err := Accept(t.Context(), &cp.DelegationMetadata{
		RootRequestId: "expired-root",
		Deadline:      timestamppb.New(time.Unix(100, 0)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FromContext(ctx).Next(ctx, "bob", "alice", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired edge returned %v", err)
	}
}

func busyView(end string) *pb.Ref {
	return &pb.Ref{
		Source: &pb.Source{
			AgentId: "alice",
			TapeId:  "tape",
		},
		Scopes: []*pb.EntryRange{
			{
				SeqS: "2",
				SeqE: end,
			},
		},
	}
}
