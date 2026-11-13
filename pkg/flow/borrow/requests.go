package borrow

import (
	"context"
	"fmt"
	"time"

	"github.com/anra-studio/anra/pkg/flow/grant"

	"buf.build/go/protovalidate"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/samber/lo"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/proto"
)

// issueGrant invokes the native handler once over all selected views. The lease
// is a candidate until the Journal commits it with its request result.
func issueGrant(ctx context.Context, view *tv.OverView, caller string, ttl time.Duration) (*pb.Lease, error) {
	result, err := view.Handle(
		ctx,
		grant.Action,
		grant.Request{
			Caller: caller,
			TTL:    ttl,
		},
	)
	if err != nil {
		return nil, recordError(err)
	}
	lease, ok := result.(*pb.Lease)
	if !ok || lease == nil {
		return nil, fmt.Errorf("%w: invalid grant handler result", ErrStorage)
	}
	return lease, nil
}

// Borrow admits a new request or replays its immutable recorded outcome.
func (s *Service) Borrow(ctx context.Context, caller string, req *pb.BorrowRequest) (*pb.Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if caller == "" {
		return nil, ErrUnavailable
	}
	if err := protovalidate.Validate(req); err != nil {
		return nil, fmt.Errorf("%w: borrow request: %w", ErrInvalidParams, err)
	}
	state, found, err := s.config.Journal.request(ctx, caller, req.RequestId)
	if err != nil {
		return nil, recordError(err)
	}
	if found {
		return state.Decision, nil
	}
	selection, err := s.config.Review(ctx, caller, req)
	if err != nil {
		return nil, err
	}
	if err := s.validate.Struct(selection); err != nil {
		return nil, fmt.Errorf("%w: review outcome: %w", ErrInvalidParams, err)
	}
	decision := &pb.Decision{
		RequestId: req.RequestId,
		Status:    selection.Status,
	}
	if selection.Status == Granted {
		if err := (grant.Request{
			Caller: caller,
			TTL:    selection.TTL,
		}).Validate(); err != nil {
			return nil, recordError(err)
		}
		target, err := s.selectView(ctx, caller, req)
		if err != nil {
			return nil, err
		}
		if target == nil {
			decision.Status = Denied
		} else {
			decision.Lease, err = issueGrant(ctx, target, caller, selection.TTL)
			if err != nil {
				return nil, err
			}
		}
	}
	result, err := s.config.Journal.admit(ctx, caller, req, decision)
	return result, recordError(err)
}

// selectView loads every complete committed scope through one native OverView.
// Handler dispatch preserves scope order and never reads the gaps between them.
func (s *Service) selectView(ctx context.Context, caller string, req *pb.BorrowRequest) (*tv.OverView, error) {
	ref, err := s.config.Views.View(ctx, caller, req.View)
	if err != nil {
		return nil, err
	}
	if ref == nil {
		return nil, nil
	}
	if err := protovalidate.Validate(ref); err != nil {
		return nil, fmt.Errorf("%w: committed view: %w", ErrInvalidParams, err)
	}
	if !proto.Equal(ref.Source, s.config.Source) || (req.View != nil && !proto.Equal(req.View, ref)) {
		return nil, ErrInvalidParams
	}
	if !withinBounds(ref, req.Bounds) {
		return nil, nil
	}

	state, err := s.config.Tape.Get(owner.WithOwnerId(ctx, s.config.Tape.OwnerID))
	if err != nil {
		return nil, recordError(err)
	}
	if !validGrantScopes(ref, state.Scope.SeqE) {
		return nil, ErrInvalidParams
	}
	scopes := lo.Map(
		ref.Scopes,
		func(scope *pb.EntryRange, _ int) tv.EntryRange {
			return tv.EntryRange{
				SeqS: te.MustParseSeq(scope.SeqS),
				SeqE: te.MustParseSeq(scope.SeqE),
			}
		},
	)
	view, err := s.config.Tape.OverView(owner.WithOwnerId(ctx, s.config.Tape.OwnerID), scopes...)
	if err != nil {
		return nil, recordError(err)
	}
	if len(view.Views) != len(scopes) {
		return nil, fmt.Errorf("%w: native view count mismatch", ErrStorage)
	}
	for i, part := range view.Views {
		if part == nil || part.Owner != state.Owner || part.SessionId != state.SessionID || part.Scope != scopes[i] {
			return nil, fmt.Errorf("%w: native view metadata mismatch", ErrStorage)
		}
	}
	return &view, nil
}

// validGrantScopes checks domain Ref ordering and physical bounds after schema
// validation. Native OverView preserves these scopes without sorting or merging.
func validGrantScopes(ref *pb.Ref, head te.Seq) bool {
	for i, scope := range ref.Scopes {
		if te.MustParseSeq(scope.SeqE).Cmp(head.Next()) > 0 {
			return false
		}
		if i > 0 && te.MustParseSeq(ref.Scopes[i-1].SeqE).Cmp(te.MustParseSeq(scope.SeqS)) > 0 {
			return false
		}
	}
	return true
}

func (s *Service) Get(ctx context.Context, caller, id string) (*pb.Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, found, err := s.config.Journal.request(ctx, caller, id)
	if err != nil {
		return nil, recordError(err)
	}
	if caller == "" || !found {
		return nil, ErrUnavailable
	}
	return st.Decision, nil
}

// Approve issues a lease for a pending request's eligible committed view.
// Missing context and failed issuance leave the request pending; only Reject
// records a local rejection. The Journal rechecks the snapshot at commit time.
func (s *Service) Approve(ctx context.Context, caller, id string, ttl time.Duration) (*pb.Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.config.Journal.pending(ctx, caller, id)
	if err != nil {
		return nil, recordError(err)
	}
	if err := (grant.Request{
		Caller: caller,
		TTL:    ttl,
	}).Validate(); err != nil {
		return nil, recordError(err)
	}
	target, err := s.selectView(ctx, caller, state.Request)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, ErrUnavailable
	}
	lease, err := issueGrant(ctx, target, caller, ttl)
	if err != nil {
		return nil, err
	}
	decision, err := s.config.Journal.approve(ctx, state, lease)
	return decision, recordError(err)
}

func (s *Service) Reject(ctx context.Context, caller, id string) (*pb.Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	decision, err := s.config.Journal.reject(ctx, caller, id)
	return decision, recordError(err)
}

func (s *Service) Release(ctx context.Context, caller, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return recordError(s.config.Grants.Release(ctx, caller, id))
}

func (s *Service) Revoke(ctx context.Context, caller, id string) error {
	return s.Release(ctx, caller, id)
}

// withinBounds applies the request's eligibility constraint to every full
// scope; it never clips a view or fills the gaps between scopes.
func withinBounds(ref *pb.Ref, bounds *pb.EntryRange) bool {
	if bounds == nil {
		return true
	}
	for _, scope := range ref.Scopes {
		if te.MustParseSeq(scope.SeqS).Cmp(te.MustParseSeq(bounds.SeqS)) < 0 || te.MustParseSeq(scope.SeqE).Cmp(te.MustParseSeq(bounds.SeqE)) > 0 {
			return false
		}
	}
	return true
}
