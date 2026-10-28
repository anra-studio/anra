package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"uuid"

	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/model/anchor"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/proto"
)

var ErrBusy = errors.New("agent: busy; request was not accepted")
var ErrFenced = errors.New("agent: execution requires storage reconciliation")

type BusyError struct{ publication *pb.AnchorRef }

var _ a2a.BusyError = (*BusyError)(nil)

func (*BusyError) Error() string                    { return ErrBusy.Error() }
func (*BusyError) Unwrap() error                    { return ErrBusy }
func (e *BusyError) BusyPublication() *pb.AnchorRef { return anchor.CloneRef(e.publication) }

type invocation struct {
	id, epoch string
	start     te.Seq
}
type execution struct {
	source       *pb.Source
	epoch        string
	active       *invocation
	latest       *pb.AnchorRef
	publications map[string]*anchor.Publication
	seen         map[string]bool
	aborted      []tv.EntryRange
	fenced       bool
	closing      bool
	cancel       context.CancelFunc
	done         chan struct{}
}

func (a *Anra) tapeContext(ctx context.Context) context.Context {
	return owner.WithOwnerId(ctx, a.principal)
}
func (a *Anra) Source() *pb.Source {
	a.mu.Lock()
	defer a.mu.Unlock()
	return proto.Clone(a.execution.source).(*pb.Source)
}

// Publication resolves an immutable publication for an identified peer without
// entering ADK. TapeView Borrow issues and enforces requester-bound grants.
func (a *Anra) Publication(_ context.Context, requester string, ref *pb.AnchorRef) (*pb.AnchorRef, error) {
	if requester == "" {
		return nil, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if ref == nil {
		ref = a.execution.latest
	}
	if ref == nil || !proto.Equal(ref.Source, a.execution.source) || a.execution.publications[ref.Seq] == nil {
		return nil, nil
	}
	return anchor.CloneRef(ref), nil
}
func (a *Anra) apply(raw te.EntryLike) error {
	r, err := anchor.Decode(raw)
	if err != nil {
		return err
	}
	if r == nil {
		return nil
	}
	x := &a.execution
	switch r.Type {
	case "tape.initialized":
		if x.source != nil || x.active != nil || r.Source.AgentId != a.principal {
			return errors.New("agent: conflicting source identity")
		}
		x.source = proto.Clone(r.Source).(*pb.Source)
	case "invocation.started":
		if x.source == nil || x.active != nil || x.seen[r.InvocationID] || !proto.Equal(r.BasePublication, x.latest) {
			return errors.New("agent: inconsistent invocation start")
		}
		x.seen[r.InvocationID] = true
		x.active = &invocation{id: r.InvocationID, epoch: r.OwnerEpoch, start: raw.GetID()}
	case "context.published", "invocation.closed":
		if x.active == nil || x.active.id != r.InvocationID || x.active.start.String() != r.StartSeq {
			return errors.New("agent: unmatched invocation record")
		}
		if r.Publication != nil {
			for _, coverage := range r.Publication.Covers {
				if te.MustParseSeq(coverage.SeqS).Cmp(x.active.start) <= 0 {
					return errors.New("agent: coverage outside invocation")
				}
			}
			x.publications[raw.GetID().String()] = r.Publication
			x.latest = &pb.AnchorRef{Source: proto.Clone(x.source).(*pb.Source), Seq: raw.GetID().String()}
		}
		if r.Type == "invocation.closed" {
			if r.Outcome != "completed" {
				x.aborted = append(x.aborted, tv.EntryRange{SeqS: x.active.start, SeqE: raw.GetID().Next()})
			}
			x.active = nil
		}
	}
	return nil
}
func (a *Anra) appendAnchor(ctx context.Context, r anchor.Record) error {
	r.Schema = anchor.Schema
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	stored, err := a.tape.Store(a.tapeContext(ctx), te.NewAnchor(te.Seq{}, a.principal, te.AnchorKindCustom, data))
	if err != nil {
		a.execution.fenced = true
		return fmt.Errorf("%w: append: %w", ErrFenced, err)
	}
	if err := a.apply(stored); err != nil {
		a.execution.fenced = true
		return fmt.Errorf("%w: %w", ErrFenced, err)
	}
	return nil
}
func (a *Anra) recoverExecution() error {
	a.execution = execution{epoch: uuid.New().String(), publications: map[string]*anchor.Publication{}, seen: map[string]bool{}}
	ctx, cancel := context.WithTimeout(a.tapeContext(context.Background()), 5*time.Second)
	defer cancel()
	state, err := a.tape.Get(ctx)
	if err != nil {
		return err
	}
	if !state.Scope.SeqE.IsZero() {
		entries, err := a.tape.Range(ctx, tv.EntryRange{SeqS: te.SeqFromUint64(1), SeqE: state.Scope.SeqE.Next()})
		if err != nil {
			return err
		}
		for _, e := range entries.Raw {
			if err := a.apply(e); err != nil {
				return fmt.Errorf("agent: replay %s: %w", e.GetID(), err)
			}
		}
	}
	if a.execution.source == nil {
		if err := a.appendAnchor(ctx, anchor.Record{Type: "tape.initialized", Source: &pb.Source{AgentId: a.principal, TapeId: uuid.New().String()}}); err != nil {
			return err
		}
	}
	if active := a.execution.active; active != nil {
		if err := a.appendAnchor(ctx, anchor.Record{Type: "invocation.closed", InvocationID: active.id, StartSeq: active.start.String(), OwnerEpoch: a.execution.epoch, Outcome: "abandoned"}); err != nil {
			return err
		}
	}
	return nil
}
func (a *Anra) admit(ctx context.Context, cancel context.CancelFunc) (chan struct{}, error) {
	identified := a2a.Requester(ctx) != ""
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.execution.closing {
		return nil, ErrClosing
	}
	if a.execution.fenced {
		return nil, ErrFenced
	}
	if a.execution.active != nil {
		var ref *pb.AnchorRef
		if identified {
			ref = anchor.CloneRef(a.execution.latest)
		}
		return nil, &BusyError{publication: ref}
	}
	if err := a.appendAnchor(ctx, anchor.Record{Type: "invocation.started", InvocationID: uuid.New().String(), OwnerEpoch: a.execution.epoch, BasePublication: anchor.CloneRef(a.execution.latest)}); err != nil {
		return nil, err
	}
	a.execution.cancel = cancel
	a.execution.done = make(chan struct{})
	return a.execution.done, nil
}

func (a *Anra) finish(answer string, runErr error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	active := a.execution.active
	if active == nil || a.execution.fenced {
		return ErrFenced
	}
	r := anchor.Record{Type: "invocation.closed", InvocationID: active.id, StartSeq: active.start.String(), Outcome: "completed"}
	if runErr != nil {
		r.Outcome = "failed"
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			r.Outcome = "cancelled"
		}
	} else if publishableAnswer(answer) {
		state, err := a.tape.Get(a.tapeContext(ctx))
		if err != nil {
			a.execution.fenced = true
			return err
		}
		// Only the completed final assistant entry is published; no private interval.
		head := state.Scope.SeqE
		entries, err := a.tape.Range(a.tapeContext(ctx), tv.EntryRange{SeqS: head, SeqE: head.Next()})
		if err != nil {
			a.execution.fenced = true
			return err
		}
		if len(entries.Raw) == 1 && entries.Raw[0].GetKind() == te.EntryAssistant && entries.Raw[0].GetSummary() == answer {
			r.Publication = &anchor.Publication{Summary: answer, SourceHead: head.String(), Covers: []*pb.EntryRange{{SeqS: head.String(), SeqE: head.Next().String()}}}
		}
	}
	if err := a.appendAnchor(ctx, r); err != nil {
		return err
	}
	if runErr != nil {
		if err := a.initialize(); err != nil {
			a.execution.fenced = true
			return err
		}
	}
	return nil
}

// Keep the repeated summary and payload within the existing exported-entry limit.
// Oversized answers still complete; they simply do not become publications.
func publishableAnswer(answer string) bool {
	if len(answer) > 2048 {
		return false
	}
	encoded, err := json.Marshal(answer)
	return err == nil && len(encoded) <= 3072
}
