package borrow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"buf.build/go/protovalidate"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/go-playground/validator/v10"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type borrowingState struct {
	Requester string
	Request   *pb.BorrowRequest
	Decision  *pb.Decision
	Released  bool
}

type requestKey struct {
	caller string
	id     string
}

// borrowingRecord is the borrowing sidecar's decision record. Protocol messages
// retain their schema validation and protobuf JSON representation. The complete
// outcome is one append, so current state and audit history cannot diverge.
type borrowingRecord struct {
	Requester string          `json:"requester" validate:"required"`
	Request   json.RawMessage `json:"request" validate:"required"`
	Decision  json.RawMessage `json:"decision" validate:"required"`
	Released  bool            `json:"released,omitempty"`
}

// borrowings is an incremental projection, never a second persistent source of
// truth. Journal.mu protects it and refreshes the sidecar before reading
// or committing. No projection is published from a partially validated tail.
type borrowings struct {
	sidecar  *sidecar
	head     te.Seq
	requests map[requestKey]borrowingState
	leases   map[string]requestKey
	validate *validator.Validate
}

func newBorrowings(sidecar *sidecar) *borrowings {
	return &borrowings{
		sidecar:  sidecar,
		requests: make(map[requestKey]borrowingState),
		leases:   make(map[string]requestKey),
		validate: validator.New(),
	}
}

func (r *borrowings) refresh(ctx context.Context) error {
	batch, head, err := r.sidecar.snapshot(ctx, r.head)
	if err != nil {
		return err
	}
	// Validate the entire tail before publishing it. In particular a malformed
	// later event must not advance a partially applied projection's head.
	pending := make(map[requestKey]borrowingState)
	leases := make(map[string]requestKey)
	for _, record := range batch {
		st, err := r.decode([]byte(record.GetSummary()))
		if err != nil {
			return fmt.Errorf("%w: lease sidecar record %s: %w", ErrStorage, record.GetID(), err)
		}
		k := requestKey{
			caller: st.Requester,
			id:     st.Request.RequestId,
		}
		prior, found := pending[k]
		if !found {
			prior, found = r.requests[k]
		}
		if !validSuccessor(prior, found, st) {
			return fmt.Errorf("%w: invalid borrowing transition at %s", ErrStorage, record.GetID())
		}
		if lease := st.Decision.Lease; lease != nil {
			priorKey, exists := leases[lease.Id]
			if !exists {
				priorKey, exists = r.leases[lease.Id]
			}
			if exists && priorKey != k {
				return fmt.Errorf("%w: reused lease ID", ErrStorage)
			}
			leases[lease.Id] = k
		}
		pending[k] = st
	}
	for k, st := range pending {
		r.requests[k] = st
	}
	for id, k := range leases {
		r.leases[id] = k
	}
	r.head = head
	return nil
}

func (r *borrowings) decode(data []byte) (borrowingState, error) {
	var record borrowingRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return borrowingState{}, err
	}
	if err := r.validate.Struct(record); err != nil {
		return borrowingState{}, err
	}
	req := new(pb.BorrowRequest)
	if err := protojson.Unmarshal(record.Request, req); err != nil {
		return borrowingState{}, err
	}
	d := new(pb.Decision)
	if err := protojson.Unmarshal(record.Decision, d); err != nil {
		return borrowingState{}, err
	}
	if err := protovalidate.Validate(req); err != nil {
		return borrowingState{}, err
	}
	if err := protovalidate.Validate(d); err != nil {
		return borrowingState{}, err
	}
	if d.RequestId != req.RequestId {
		return borrowingState{}, errors.New("decision identifies another request")
	}
	if d.Lease != nil {
		if d.Lease.Caller != record.Requester || !proto.Equal(d.Lease.Ref.Source, r.sidecar.source) {
			return borrowingState{}, errors.New("lease caller or source mismatch")
		}
	} else if record.Released {
		return borrowingState{}, errors.New("non-granted borrowing has a release")
	}
	return borrowingState{
		Requester: record.Requester,
		Request:   req,
		Decision:  d,
		Released:  record.Released,
	}, nil
}

func sameState(a, b borrowingState) bool {
	return a.Requester == b.Requester && a.Released == b.Released && proto.Equal(a.Request, b.Request) && proto.Equal(a.Decision, b.Decision)
}

// validSuccessor guards replay as well as writes. Only initial admission,
// pending resolution and a granted lease's one-way release are persisted.
func validSuccessor(prior borrowingState, found bool, next borrowingState) bool {
	if !found {
		return !next.Released && lifecycle(newState).Can(next.Decision.Status.String())
	}
	if prior.Requester != next.Requester || !proto.Equal(prior.Request, next.Request) {
		return false
	}
	if prior.Decision.Status == Pending {
		return !next.Released && lifecycle(Pending.String()).Can(next.Decision.Status.String())
	}
	return prior.Decision.Status == Granted && !prior.Released && next.Released && proto.Equal(prior.Decision, next.Decision)
}

func (r *borrowings) request(ctx context.Context, caller, id string) (borrowingState, bool, error) {
	if err := r.refresh(ctx); err != nil {
		return borrowingState{}, false, err
	}
	st, found := r.requests[requestKey{
		caller: caller,
		id:     id,
	}]
	return cloneState(st), found, nil
}

func (r *borrowings) lease(ctx context.Context, caller, id string) (borrowingState, bool, error) {
	if err := r.refresh(ctx); err != nil {
		return borrowingState{}, false, err
	}
	k, found := r.leases[id]
	if !found || k.caller != caller {
		return borrowingState{}, false, nil
	}
	return cloneState(r.requests[k]), true, nil
}

func cloneState(st borrowingState) borrowingState {
	if st.Request != nil {
		st.Request = proto.Clone(st.Request).(*pb.BorrowRequest)
	}
	if st.Decision != nil {
		st.Decision = proto.Clone(st.Decision).(*pb.Decision)
	}
	return st
}

func (r *borrowings) create(ctx context.Context, caller string, req *pb.BorrowRequest, d *pb.Decision) error {
	return r.commit(
		ctx,
		nil,
		borrowingState{
			Requester: caller,
			Request:   req,
			Decision:  d,
		},
	)
}

func (r *borrowings) complete(ctx context.Context, expected borrowingState, d *pb.Decision) error {
	next := expected
	next.Decision = d
	return r.commit(ctx, &expected, next)
}

func (r *borrowings) release(ctx context.Context, expected borrowingState) error {
	next := expected
	next.Released = true
	return r.commit(ctx, &expected, next)
}

func (r *borrowings) commit(ctx context.Context, expected *borrowingState, next borrowingState) error {
	req, err := protojson.Marshal(next.Request)
	if err != nil {
		return err
	}
	decision, err := protojson.Marshal(next.Decision)
	if err != nil {
		return err
	}
	data, err := json.Marshal(
		borrowingRecord{
			Requester: next.Requester,
			Request:   req,
			Decision:  decision,
			Released:  next.Released,
		},
	)
	if err != nil {
		return err
	}
	// The same decoder validates writes and recovery.
	if _, err := r.decode(data); err != nil {
		return fmt.Errorf("%w: borrowing outcome: %w", ErrStorage, err)
	}
	k := requestKey{
		caller: next.Requester,
		id:     next.Request.RequestId,
	}
	if err := r.refresh(ctx); err != nil {
		return err
	}
	prior, found := r.requests[k]
	if (expected == nil && found) || (expected != nil && (!found || !sameState(prior, *expected))) {
		return ErrConflict
	}
	if !validSuccessor(prior, found, next) {
		return ErrConflict
	}
	if lease := next.Decision.Lease; lease != nil {
		if priorKey, exists := r.leases[lease.Id]; exists && priorKey != k {
			return ErrConflict
		}
	}
	return r.sidecar.append(ctx, r.head, data)
}
