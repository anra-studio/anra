package view

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
	"uuid"

	"buf.build/go/protovalidate"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/anra-studio/anra/pkg/model/grantRecord"
	"github.com/go-playground/validator/v10"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Selection decides admission and lifetime, never an arbitrary Tape range.
type Selection struct {
	Status pb.DecisionStatus
	TTL    time.Duration
}

// Config borrows initialized dependencies. RecordPath opens an owned store;
// Records supplies a caller-owned store instead. Dependencies and provider
// results must remain immutable while the service uses them.
type Config struct {
	Source     *pb.Source                                                          `validate:"required,structonly"`
	Owner      string                                                              `validate:"required"`
	Tape       TapeReader                                                          `validate:"required,structonly"`
	Views      Views                                                               `validate:"required,structonly"`
	RecordPath string                                                              `validate:"required_without=Records,excluded_with=Records"`
	Records    *grantRecord.Store                                                  `validate:"required_without=RecordPath,excluded_with=RecordPath,structonly"`
	Exporter   Exporter                                                            `validate:"-"`
	Now        func() time.Time                                                    `validate:"-"`
	Approve    func(context.Context, string, *pb.BorrowRequest) (Selection, error) `validate:"-"`
}

// Service orders lease decisions and read authorization. It never enters a
// reasoning turn or changes the source Tape. Both adapters share this instance.
type Service struct {
	mu           sync.Mutex
	config       Config
	closeRecords func() error
}

func NewService(c Config) (*Service, error) {
	if err := validator.New(validator.WithRequiredStructEnabled()).Struct(c); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidParams, err)
	}
	if err := protovalidate.Validate(c.Source); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidParams, err)
	}
	var closer func() error
	if c.Records == nil {
		r, err := grantRecord.Open(c.RecordPath, c.Source)
		if err != nil {
			return nil, err
		}
		c.Records = r
		closer = r.Close
	} else if !proto.Equal(c.Records.Source(), c.Source) {
		return nil, ErrInvalidParams
	}
	if c.Exporter == nil {
		c.Exporter = ContextExporter{}
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &Service{
		config:       c,
		closeRecords: closer,
	}, nil
}

func (s *Service) Close() error {
	if s.closeRecords != nil {
		return s.closeRecords()
	}
	return nil
}

func recordError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, grantRecord.ErrConflict) {
		return ErrConflict
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrStorage, err)
}

// Borrow selects a complete committed Ref after approval. Identical requests
// replay their recorded decision without changing the lease or its expiry.
func (s *Service) Borrow(ctx context.Context, caller string, req *pb.BorrowRequest) (*pb.Decision, error) {
	return s.borrow(ctx, caller, req, nil)
}

func (s *Service) borrow(ctx context.Context, caller string, req *pb.BorrowRequest, approved *Selection) (*pb.Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if caller == "" {
		return nil, ErrUnavailable
	}
	if err := protovalidate.Validate(req); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidParams, err)
	}
	st, found, err := s.config.Records.RequestGrant(ctx, caller, req.RequestId)
	if err != nil {
		return nil, recordError(err)
	}
	if found {
		if !proto.Equal(st.Request, req) {
			return nil, ErrConflict
		}
		return st.Decision, nil
	}
	sel := Selection{
		Status: Granted,
		TTL:    time.Hour,
	}
	if approved != nil {
		sel = *approved
	} else if s.config.Approve != nil {
		sel, err = s.config.Approve(ctx, caller, req)
		if err != nil {
			return nil, err
		}
	}
	return s.decide(ctx, caller, req, sel, nil)
}

func (s *Service) decide(ctx context.Context, caller string, req *pb.BorrowRequest, sel Selection, previous *grantRecord.State) (*pb.Decision, error) {
	d := &pb.Decision{
		RequestId: req.RequestId,
		Status:    sel.Status,
	}
	if sel.Status == Granted {
		if sel.TTL <= 0 || sel.TTL > 24*time.Hour {
			return nil, ErrInvalidParams
		}
		ref, err := s.config.Views.View(ctx, caller, req.View)
		if err != nil {
			return nil, err
		}
		if ref == nil {
			d.Status = Denied
		} else {
			if !proto.Equal(ref.Source, s.config.Source) || (req.View != nil && !proto.Equal(req.View, ref)) {
				return nil, ErrInvalidParams
			}
			if req.Bounds != nil {
				for _, r := range ref.Scopes {
					if te.MustParseSeq(r.SeqS).Cmp(te.MustParseSeq(req.Bounds.SeqS)) < 0 || te.MustParseSeq(r.SeqE).Cmp(te.MustParseSeq(req.Bounds.SeqE)) > 0 {
						d.Status = Denied
					}
				}
			}
			if d.Status == Granted {
				d.Lease = &pb.Lease{
					Id:        uuid.New().String(),
					Caller:    caller,
					Ref:       proto.Clone(ref).(*pb.Ref),
					ExpiresAt: timestamppb.New(s.config.Now().Add(sel.TTL)),
				}
			}
		}
	} else if sel.Status != Denied && sel.Status != Pending {
		return nil, ErrInvalidParams
	}
	if err := s.config.Records.Decide(ctx, caller, req, d, previous); err != nil {
		return nil, recordError(err)
	}
	return d, nil
}

// Grant is a trusted local operation; commitment is still resolved by Views.
func (s *Service) Grant(ctx context.Context, caller, requestID string, ref *pb.Ref) (*pb.Lease, error) {
	if ref == nil {
		return nil, ErrInvalidParams
	}
	d, err := s.borrow(
		ctx,
		caller,
		&pb.BorrowRequest{
			RequestId: requestID,
			Purpose:   "controller handoff",
			Query:     "committed view",
			View:      ref,
		},
		&Selection{
			Status: Granted,
			TTL:    time.Hour,
		},
	)
	if err != nil {
		return nil, err
	}
	if d.Status != Granted {
		return nil, ErrUnavailable
	}
	return d.Lease, nil
}

func (s *Service) Get(ctx context.Context, caller, id string) (*pb.Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok, err := s.config.Records.RequestGrant(ctx, caller, id)
	if err != nil {
		return nil, recordError(err)
	}
	if caller == "" || !ok {
		return nil, ErrUnavailable
	}
	return st.Decision, nil
}

func (s *Service) Resolve(ctx context.Context, caller, id string, sel Selection) (*pb.Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok, err := s.config.Records.RequestGrant(ctx, caller, id)
	if err != nil {
		return nil, recordError(err)
	}
	if !ok {
		return nil, ErrUnavailable
	}
	if st.Decision.Status != Pending {
		return nil, ErrConflict
	}
	return s.decide(ctx, caller, st.Request, sel, &st)
}

func (s *Service) authorized(ctx context.Context, caller, id string) (*pb.Lease, error) {
	st, ok, err := s.config.Records.LeaseGrant(ctx, caller, id)
	if err != nil {
		return nil, recordError(err)
	}
	if caller == "" || !ok || st.Released || !s.config.Now().Before(st.Decision.Lease.ExpiresAt.AsTime()) {
		return nil, ErrUnavailable
	}
	return st.Decision.Lease, nil
}

func (s *Service) quote(ctx context.Context, caller, id string) (*Quote, error) {
	lease, err := s.authorized(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	q := &Quote{
		Channel: Borrowed,
		Provenance: Provenance{
			Caller:  lease.Caller,
			LeaseID: lease.Id,
			Ref:     lease.Ref,
		},
		Entries: []Entry{},
	}
	ctx = owner.WithOwnerId(ctx, s.config.Owner)
	for _, scope := range lease.Ref.Scopes {
		r := tv.EntryRange{
			SeqS: te.MustParseSeq(scope.SeqS),
			SeqE: te.MustParseSeq(scope.SeqE),
		}
		batch, err := s.config.Tape.Range(ctx, r)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrStorage, err)
		}
		next := r.SeqS
		for _, raw := range batch.Raw {
			if raw == nil || raw.GetOwner() != s.config.Owner || raw.GetID() != next || next.Cmp(r.SeqE) >= 0 {
				return nil, ErrStorage
			}
			e, err := s.config.Exporter.Export(raw)
			if err != nil {
				return nil, err
			}
			q.Entries = append(q.Entries, e)
			next = next.Next()
		}
		if next != r.SeqE {
			return nil, ErrStorage
		}
	}
	return q, nil
}

// ReadAll authorizes against the current lease and reads every scope atomically
// from the caller's perspective: no partial Quote is returned on a failed range.
func (s *Service) ReadAll(ctx context.Context, caller, id string) (*Quote, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.quote(ctx, caller, id)
}

type cursor struct {
	LeaseID string `json:"leaseId"`
	Offset  int    `json:"offset"`
}

// Read returns whole public entries from one lease, preserving the immutable Ref
// across page boundaries. A continuation cannot select another lease or range.
func (s *Service) Read(ctx context.Context, caller, id, continuation string, size int32) (*Quote, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if size < 0 || size > MaxPageSize || len(continuation) > 1024 {
		return nil, ErrInvalidParams
	}
	q, err := s.quote(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	if size == 0 {
		size = 32
	}
	offset := 0
	if continuation != "" {
		data, err := base64.RawURLEncoding.DecodeString(continuation)
		if err != nil {
			return nil, ErrInvalidParams
		}
		var c cursor
		if json.Unmarshal(data, &c) != nil || c.LeaseID != id || c.Offset < 1 || c.Offset >= len(q.Entries) {
			return nil, ErrInvalidParams
		}
		offset = c.Offset
	}
	end := min(offset+int(size), len(q.Entries))
	if end < len(q.Entries) {
		data, err := json.Marshal(
			cursor{
				LeaseID: id,
				Offset:  end,
			},
		)
		if err != nil {
			return nil, err
		}
		q.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	q.Entries = q.Entries[offset:end]
	return q, nil
}

func (s *Service) Release(ctx context.Context, caller, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok, err := s.config.Records.LeaseGrant(ctx, caller, id)
	if err != nil {
		return recordError(err)
	}
	if caller == "" || !ok {
		return ErrUnavailable
	}
	if st.Released {
		return nil
	}
	return recordError(s.config.Records.Release(ctx, st))
}

func (s *Service) Revoke(ctx context.Context, caller, id string) error {
	return s.Release(ctx, caller, id)
}
