package borrow

import (
	"context"
	"fmt"
	"sync"

	"buf.build/go/protovalidate"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/anra-studio/anra/pkg/flow/grant"
	"github.com/go-playground/validator/v10"
	"github.com/scbizu/tape-go/pkg/tape/storage"
	"google.golang.org/protobuf/proto"
)

// Journal is the sole owner of persisted request decisions and their leases.
// Every transition is one sidecar append under mu. Share one Journal across
// services and the grant authorizer. The host owns the injected storage.
type Journal struct {
	mu      sync.Mutex
	records *borrowings
}

type JournalConfig struct {
	Source  *pb.Source          `validate:"required"`
	Owner   string              `validate:"required"`
	Storage storage.TapeStorage `validate:"required"`
}

func NewJournal(ctx context.Context, c JournalConfig) (*Journal, error) {
	if err := validator.New().Struct(c); err != nil {
		return nil, fmt.Errorf("%w: journal config: %w", ErrInvalidParams, err)
	}
	if err := protovalidate.Validate(c.Source); err != nil {
		return nil, fmt.Errorf("%w: journal source: %w", ErrInvalidParams, err)
	}
	store := &sidecar{
		storage: c.Storage,
		owner:   c.Owner,
		source:  proto.Clone(c.Source).(*pb.Source),
	}
	records := newBorrowings(store)
	if err := records.refresh(ctx); err != nil {
		return nil, err
	}
	return &Journal{
		records: records,
	}, nil
}

func (j *Journal) Source() *pb.Source { return proto.Clone(j.records.sidecar.source).(*pb.Source) }
func (j *Journal) Owner() string      { return j.records.sidecar.owner }

func (j *Journal) request(ctx context.Context, caller, id string) (borrowingState, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.records.request(ctx, caller, id)
}

// admit replays an existing request or commits its first decision atomically.
func (j *Journal) admit(ctx context.Context, caller string, req *pb.BorrowRequest, decision *pb.Decision) (*pb.Decision, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	current, found, err := j.records.request(ctx, caller, req.RequestId)
	if err != nil {
		return nil, err
	}
	if found {
		return current.Decision, nil
	}
	if err := j.records.create(ctx, caller, req, decision); err != nil {
		return nil, err
	}
	return decision, nil
}

func (j *Journal) pending(ctx context.Context, caller, id string) (borrowingState, error) {
	state, found, err := j.request(ctx, caller, id)
	if err != nil {
		return borrowingState{}, err
	}
	if caller == "" || !found {
		return borrowingState{}, ErrUnavailable
	}
	if state.Decision.Status != Pending {
		return borrowingState{}, ErrConflict
	}
	return state, nil
}

func (j *Journal) approve(ctx context.Context, expected borrowingState, lease *pb.Lease) (*pb.Decision, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	decision := &pb.Decision{
		RequestId: expected.Request.RequestId,
		Status:    Granted,
		Lease:     lease,
	}
	if err := j.records.complete(ctx, expected, decision); err != nil {
		return nil, err
	}
	return decision, nil
}

func (j *Journal) reject(ctx context.Context, caller, id string) (*pb.Decision, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	state, found, err := j.records.request(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	if caller == "" || !found {
		return nil, ErrUnavailable
	}
	if state.Decision.Status != Pending {
		return nil, ErrConflict
	}
	decision := &pb.Decision{
		RequestId: id,
		Status:    Denied,
	}
	if err := j.records.complete(ctx, state, decision); err != nil {
		return nil, err
	}
	return decision, nil
}

// Lease implements grant.Leases using the same journal as request approval.
func (j *Journal) Lease(ctx context.Context, caller, id string) (grant.State, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	state, found, err := j.records.lease(ctx, caller, id)
	if err != nil || !found {
		return grant.State{}, found, err
	}
	return grant.State{
		Lease:    state.Decision.Lease,
		Released: state.Released,
	}, true, nil
}

func (j *Journal) Release(ctx context.Context, caller, id string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	state, found, err := j.records.lease(ctx, caller, id)
	if err != nil {
		return err
	}
	if caller == "" || !found {
		return grant.ErrUnavailable
	}
	if state.Released {
		return nil
	}
	return j.records.release(ctx, state)
}
