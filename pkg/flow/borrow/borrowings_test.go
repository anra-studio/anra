package borrow

import (
	"testing"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
)

func TestReleasePreservesIssuedBorrowing(t *testing.T) {
	t.Parallel()
	issued := borrowingState{
		Requester: "caller",
		Request: &pb.BorrowRequest{
			RequestId: "request",
			Purpose:   "research",
		},
		Decision: &pb.Decision{
			RequestId: "request",
			Status:    Granted,
			Lease: &pb.Lease{
				Id:     "lease",
				Caller: "caller",
				Ref: &pb.Ref{
					Source: &pb.Source{
						AgentId: "agent",
						TapeId:  "tape",
					},
					Scopes: []*pb.EntryRange{
						{
							SeqS: "9",
							SeqE: "12",
						},
					},
				},
			},
		},
	}
	released := cloneState(issued)
	released.Released = true
	if !validSuccessor(issued, true, released) {
		t.Fatal("an unchanged issued borrowing must be releasable")
	}
	if validSuccessor(released, true, issued) || validSuccessor(released, true, released) {
		t.Fatal("a released borrowing cannot be reactivated or committed twice")
	}
	changedRequest := cloneState(released)
	changedRequest.Request.Purpose = "different purpose"
	if validSuccessor(issued, true, changedRequest) {
		t.Fatal("release rewrote the original request")
	}
	expandedView := cloneState(released)
	expandedView.Decision.Lease.Ref.Scopes[0].SeqE = "99"
	if validSuccessor(issued, true, expandedView) {
		t.Fatal("release expanded the issued view")
	}
	changedOwner := cloneState(released)
	changedOwner.Requester = "other"
	if validSuccessor(issued, true, changedOwner) {
		t.Fatal("release transferred the borrowing to another caller")
	}
	if validSuccessor(borrowingState{}, false, released) {
		t.Fatal("a release cannot create a borrowing")
	}
}
