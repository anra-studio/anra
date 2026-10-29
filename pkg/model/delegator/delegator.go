// Package delegator owns an invocation's collaboration path and progress.
// Protobuf defines the wire data; this model controls its runtime transitions.
package delegator

import (
	"context"
	"errors"
	"sync"
	"time"
	"uuid"

	"buf.build/go/protovalidate"
	cp "github.com/scbizu/anra/gen/anra/a2a/collaboration/v1"
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"github.com/scbizu/tape-go/pkg/tape/entry"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const maxInvocationTime = 2 * time.Minute

var ErrNoNewContext = errors.New("delegator: no new context from busy peer")

type contextKey struct{}

// Delegator owns an immutable path and the invocation's shared BUSY memory.
// Next forks the path for one outgoing edge; Begin creates fresh invocation state.
type Delegator struct {
	metadata *cp.DelegationMetadata
	busy     *busyPeers
}

type busyPeers struct {
	mu    sync.Mutex
	peers map[string]*pb.TapeView
}

// Accept validates inbound metadata and records an owned copy in the context.
func Accept(ctx context.Context, metadata *cp.DelegationMetadata) (context.Context, error) {
	if metadata == nil {
		return ctx, errors.New("delegator: missing metadata")
	}
	if err := protovalidate.Validate(metadata); err != nil {
		return ctx, err
	}
	d := newDelegator(proto.Clone(metadata).(*cp.DelegationMetadata))
	return context.WithValue(ctx, contextKey{}, d), nil
}

// Begin caps the deadline and starts fresh BUSY memory for a local invocation.
// An inherited root, path, and context references survive this transition.
func Begin(ctx context.Context) (context.Context, context.CancelFunc) {
	metadata := FromContext(ctx).Metadata()
	deadline := boundedDeadline(ctx)
	if inherited := metadata.Deadline.AsTime(); inherited.Before(deadline) {
		deadline = inherited
	}
	metadata.Deadline = timestamppb.New(deadline)
	ctx = context.WithValue(ctx, contextKey{}, newDelegator(metadata))
	return context.WithDeadline(ctx, deadline)
}

// FromContext returns the current invocation's model, or a new root when the
// caller is outside an agent invocation (for example, a CLI request).
func FromContext(ctx context.Context) *Delegator {
	if d, ok := ctx.Value(contextKey{}).(*Delegator); ok {
		return d
	}
	return newDelegator(&cp.DelegationMetadata{RootRequestId: uuid.New().String(), Deadline: timestamppb.New(boundedDeadline(ctx))})
}

func newDelegator(metadata *cp.DelegationMetadata) *Delegator {
	return &Delegator{metadata: metadata, busy: &busyPeers{peers: map[string]*pb.TapeView{}}}
}

func boundedDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(maxInvocationTime)
	if local, ok := ctx.Deadline(); ok && local.Before(deadline) {
		deadline = local
	}
	return deadline
}

// Metadata returns an owned snapshot suitable for transport or model input.
func (d *Delegator) Metadata() *cp.DelegationMetadata {
	return proto.Clone(d.metadata).(*cp.DelegationMetadata)
}

// Next prepares one edge without modifying the current or any sibling path.
// No network call is permitted when the peer has unchanged BUSY context or the
// deadline has expired. References are hints, never transferable grants.
func (d *Delegator) Next(ctx context.Context, self, peer string, refs []*pb.TapeView) (context.Context, error) {
	d.busy.mu.Lock()
	_, seen := d.busy.peers[peer]
	d.busy.mu.Unlock()
	if seen {
		return ctx, ErrNoNewContext
	}
	if err := ctx.Err(); err != nil {
		return ctx, err
	}
	if !time.Now().Before(d.metadata.Deadline.AsTime()) {
		return ctx, context.DeadlineExceeded
	}
	next := d.Metadata()
	if len(refs) > 0 {
		next.Context = nil
		for _, ref := range refs {
			if ref == nil {
				return ctx, errors.New("delegator: nil context reference")
			}
			next.Context = append(next.Context, proto.Clone(ref).(*pb.TapeView))
		}
	}
	next.Ancestors = append(next.Ancestors, self)
	if err := protovalidate.Validate(next); err != nil {
		return ctx, err
	}
	edge := &Delegator{metadata: next, busy: d.busy}
	return context.WithValue(ctx, contextKey{}, edge), nil
}

// Observe records a validated peer outcome. A BUSY view is copied so
// later changes to transport results cannot change invocation-local memory.
func (d *Delegator) Observe(peer string, response *cp.ResponseMetadata) {
	if response.GetStatus() != cp.Status_STATUS_BUSY {
		return
	}
	var ref *pb.TapeView
	if response.View != nil {
		ref = proto.Clone(response.View).(*pb.TapeView)
	}
	d.busy.mu.Lock()
	d.busy.peers[peer] = ref
	d.busy.mu.Unlock()
}

// Borrowed records progress only after the granted context has been read. An
// unchanged or older snapshot keeps reasoning suppressed for that peer.
func (d *Delegator) Borrowed(peer string, view *pb.View) {
	if view == nil || view.Source == nil {
		return
	}
	d.busy.mu.Lock()
	defer d.busy.mu.Unlock()
	reported, busy := d.busy.peers[peer]
	if !busy {
		return
	}
	if reported == nil || !proto.Equal(reported.Source, view.Source) {
		delete(d.busy.peers, peer)
		return
	}
	previous, previousErr := entry.ParseSeq(reported.SnapshotHead)
	next, nextErr := entry.ParseSeq(view.SnapshotHead)
	if previousErr == nil && nextErr == nil && next.Cmp(previous) > 0 {
		delete(d.busy.peers, peer)
	}
}
