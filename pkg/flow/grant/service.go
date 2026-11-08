// Package grant issues leases over native Tape views and enforces their lifetime.
// Request admission and persistence belong to its borrowing caller.
package grant

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"buf.build/go/protovalidate"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/go-playground/validator/v10"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const Action = "grant"

var (
	ErrInvalidParams = errors.New("invalid grant parameters")
	ErrUnavailable   = errors.New("lease unavailable")
	ErrStorage       = errors.New("lease storage failure")
)

// State is a committed lease snapshot. Released leases never become active.
type State struct {
	Lease    *pb.Lease
	Released bool
}

func (s State) Active(now time.Time) bool {
	return s.Lease != nil && !s.Released && now.Before(s.Lease.ExpiresAt.AsTime())
}

// Leases is consumed here and implemented by borrow.Journal. Release must
// atomically check ownership and persist termination, and remain idempotent.
// Reading and releasing use the same commit boundary as request approval.
type Leases interface {
	Lease(context.Context, string, string) (State, bool, error)
	Release(context.Context, string, string) error
}

type Config struct {
	Source *pb.Source `validate:"required"`
	Owner  string     `validate:"required"`
	Leases Leases     `validate:"required"`
	Now    func() time.Time
}

// Service is a native view.Handler and a committed-lease authorizer. Issuing a
// lease constructs a candidate; the caller must commit it with its request
// result before returning it. The service owns no storage or goroutines.
type Service struct {
	source *pb.Source
	owner  string
	leases Leases
	now    func() time.Time
}

func New(c Config) (*Service, error) {
	if err := validator.New().Struct(c); err != nil {
		return nil, fmt.Errorf("%w: config: %w", ErrInvalidParams, err)
	}
	if err := protovalidate.Validate(c.Source); err != nil {
		return nil, fmt.Errorf("%w: source: %w", ErrInvalidParams, err)
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &Service{
		source: proto.Clone(c.Source).(*pb.Source),
		owner:  c.Owner,
		leases: c.Leases,
		now:    c.Now,
	}, nil
}

func (s *Service) Action() string     { return Action }
func (s *Service) Source() *pb.Source { return proto.Clone(s.source).(*pb.Source) }
func (s *Service) Leases() Leases     { return s.leases }

func (s *Service) Owner() string { return s.owner }

// Request contains only lease issuance inputs, never a pending request state.
type Request struct {
	Caller string        `validate:"required"`
	TTL    time.Duration `validate:"gt=0,lte=86400000000000"`
}

func (r Request) Validate() error {
	if err := validator.New().Struct(r); err != nil {
		return fmt.Errorf("%w: lease request: %w", ErrInvalidParams, err)
	}
	return nil
}

func (s *Service) Handle(ctx context.Context, views []*tv.EntryView, payload any) (any, error) {
	req, ok := payload.(Request)
	if !ok {
		return nil, ErrInvalidParams
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ref, err := viewRef(s.source, s.owner, views)
	if err != nil {
		return nil, err
	}
	return &pb.Lease{
		Id:        uuid.New().String(),
		Caller:    req.Caller,
		Ref:       ref,
		ExpiresAt: timestamppb.New(s.now().Add(req.TTL)),
	}, nil
}

// Authorize observes committed state. A later release may terminate the lease
// while the already-authorized read completes; subsequent reads must fail.
func (s *Service) Authorize(ctx context.Context, caller, id string) (*pb.Lease, error) {
	if caller == "" {
		return nil, ErrUnavailable
	}
	state, found, err := s.leases.Lease(ctx, caller, id)
	if err != nil {
		return nil, fmt.Errorf("%w: lookup lease %q: %w", ErrStorage, id, err)
	}
	if !found || !state.Active(s.now()) {
		return nil, ErrUnavailable
	}
	return state.Lease, nil
}

func (s *Service) Release(ctx context.Context, caller, id string) error {
	if caller == "" {
		return ErrUnavailable
	}
	return s.leases.Release(ctx, caller, id)
}
