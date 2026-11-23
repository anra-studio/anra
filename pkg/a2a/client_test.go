package a2a

import (
	"errors"
	"strconv"
	"testing"
	"time"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestBorrowRequiresViewExtensionBeforeTransport(t *testing.T) {
	t.Parallel()

	req := &pb.BorrowRequest{
		RequestId: "borrow",
		Purpose:   "review findings",
		Query:     "research",
		View:      testViewLease().Ref,
	}
	// Without the negotiated extension the client must stop before using RPC.
	client := &Client{}
	if _, err := client.Borrow(t.Context(), req); !errors.Is(err, ErrInactive) {
		t.Fatalf("peer without the view version returned %v", err)
	}
}

func TestBorrowDecisionMustMatchRequestedView(t *testing.T) {
	t.Parallel()

	lease := testViewLease()
	request := &pb.BorrowRequest{
		RequestId: "borrow",
		View:      lease.Ref,
	}
	decision := &pb.Decision{
		RequestId: request.RequestId,
		Status:    Granted,
		Lease:     lease,
	}
	if err := validateBorrowDecision(request, decision); err != nil {
		t.Fatal(err)
	}
	wrongSource := proto.Clone(decision).(*pb.Decision)
	wrongSource.Lease.Ref.Source.TapeId = "another-tape"
	wrongScope := proto.Clone(decision).(*pb.Decision)
	wrongScope.Lease.Ref.Scopes[0].SeqE = "8"
	for _, invalid := range []*pb.Decision{wrongSource, wrongScope} {
		if err := validateBorrowDecision(request, invalid); !errors.Is(err, ErrInvalidParams) {
			t.Errorf("accepted a grant for a different view: %v, %v", invalid.Lease.Ref, err)
		}
	}
}

func TestQuoteMustCoverEveryGrantedScopeInOrder(t *testing.T) {
	t.Parallel()

	lease := testViewLease()
	lease.Ref.Scopes = nil
	quote := &pb.Quote{
		Channel: "borrowed",
		Provenance: &pb.Provenance{
			Caller:  lease.Caller,
			LeaseId: lease.Id,
			Ref:     lease.Ref,
		},
	}
	// More than a hundred disjoint segments still form one complete quote.
	for i := 1; i <= 103; i++ {
		start := strconv.Itoa(2 * i)
		lease.Ref.Scopes = append(lease.Ref.Scopes, &pb.EntryRange{
			SeqS: start,
			SeqE: strconv.Itoa(2*i + 1),
		})
		quote.Entries = append(quote.Entries, &pb.ExportedEntry{
			Seq: start,
		})
	}
	if err := validateQuoteForLease(lease, quote); err != nil {
		t.Fatal(err)
	}
	quote.Provenance.Caller = "another-caller"
	if err := validateQuoteForLease(lease, quote); !errors.Is(err, ErrInvalidParams) {
		t.Errorf("accepted another caller's quote: %v", err)
	}
	quote.Provenance.Caller = lease.Caller
	quote.Entries[101] = quote.Entries[100]
	if err := validateQuoteForLease(lease, quote); !errors.Is(err, ErrInvalidParams) {
		t.Errorf("accepted a duplicate in place of an authorized entry: %v", err)
	}
	quote.Entries = quote.Entries[:101]
	if err := validateQuoteForLease(lease, quote); !errors.Is(err, ErrInvalidParams) {
		t.Errorf("accepted a truncated quote: %v", err)
	}
}

func testViewLease() *pb.Lease {
	return &pb.Lease{
		Id:     "lease",
		Caller: "bob",
		Ref: &pb.Ref{
			Source: &pb.Source{
				AgentId: "alice",
				TapeId:  "tape",
			},
			Scopes: []*pb.EntryRange{
				{
					SeqS: "2",
					SeqE: "4",
				},
			},
		},
		ExpiresAt: timestamppb.New(time.Unix(100, 0)),
	}
}
