package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"uuid"

	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	"github.com/anra-studio/anra/pkg/a2a"
	"github.com/anra-studio/anra/pkg/model/anchor"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/proto"
)

var ErrBusy = errors.New("agent: busy; request was not accepted")
var ErrFenced = errors.New("agent: execution requires storage reconciliation")

type BusyError struct{ view *pb.TapeView }

var _ a2a.BusyError = (*BusyError)(nil)

func (*BusyError) Error() string            { return ErrBusy.Error() }
func (*BusyError) Unwrap() error            { return ErrBusy }
func (e *BusyError) BusyView() *pb.TapeView { return anchor.CloneRef(e.view) }

// activeTurn identifies the admitted Agent Turn spanning the model/tool loop.
type activeTurn struct {
	id, epoch string
	start     te.Seq
}
type execution struct {
	source    *pb.Source
	sessionID string
	epoch     string
	active    *activeTurn
	latest    *pb.TapeView
	views     map[string]tv.TapeView
	seen      map[string]bool
	aborted   []tv.EntryRange
	fenced    bool
	closing   bool
	cancel    context.CancelFunc
	done      chan struct{}
}

func (a *Anra) tapeContext(ctx context.Context) context.Context {
	return owner.WithOwnerId(ctx, a.principal)
}
func (a *Anra) Source() *pb.Source {
	a.mu.Lock()
	defer a.mu.Unlock()
	return proto.Clone(a.execution.source).(*pb.Source)
}

// View resolves an immutable view for an identified peer without
// entering ADK. TapeView Borrow issues and enforces requester-bound grants.
func (a *Anra) View(_ context.Context, requester string, ref *pb.TapeView) (*pb.TapeView, error) {
	if requester == "" {
		return nil, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if ref == nil {
		ref = a.execution.latest
	}
	if ref == nil || !proto.Equal(ref.Source, a.execution.source) {
		return nil, nil
	}
	window, ok := a.execution.views[ref.SnapshotHead]
	if !ok {
		return nil, nil
	}
	selected := a.viewRef(window)
	if !proto.Equal(ref, selected) {
		return nil, nil
	}
	return selected, nil
}
func (a *Anra) viewRef(window tv.TapeView) *pb.TapeView {
	return &pb.TapeView{
		Source:       proto.Clone(a.execution.source).(*pb.Source),
		SnapshotHead: window.Scope.SeqE.String(),
		Scope:        &pb.EntryRange{SeqS: window.Scope.SeqS.String(), SeqE: window.Scope.SeqE.String()},
	}
}
func (a *Anra) apply(raw te.EntryLike) error {
	r, err := anchor.Decode(raw)
	if err != nil {
		return err
	}
	if r == nil {
		// Other schemas do not drive the native execution state machine.
		return nil
	}
	x := &a.execution
	switch r.Type {
	case anchor.TapeInitialized:
		if x.source != nil || x.active != nil || r.Source.AgentId != a.principal {
			return errors.New("agent: conflicting source identity")
		}
		x.source = proto.Clone(r.Source).(*pb.Source)
	case anchor.TurnStarted:
		if x.source == nil || x.active != nil || x.seen[r.TurnID] || !proto.Equal(r.BaseView, x.latest) {
			return errors.New("agent: inconsistent turn start")
		}
		x.seen[r.TurnID] = true
		x.active = &activeTurn{id: r.TurnID, epoch: r.OwnerEpoch, start: raw.GetID()}
	case anchor.TurnClosed:
		if x.active == nil || x.active.id != r.TurnID || x.active.start.String() != r.StartSeq {
			return errors.New("agent: unmatched turn record")
		}
		if r.Outcome == "completed" {
			window := tv.TapeView{SessionID: x.sessionID, Owner: a.principal, Scope: tv.EntryRange{SeqS: x.active.start.Next(), SeqE: raw.GetID()}}
			x.views[raw.GetID().String()] = window
			x.latest = a.viewRef(window)
		} else {
			x.aborted = append(x.aborted, tv.EntryRange{SeqS: x.active.start, SeqE: raw.GetID().Next()})
		}
		x.active = nil
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
	a.execution = execution{epoch: uuid.New().String(), views: map[string]tv.TapeView{}, seen: map[string]bool{}}
	ctx, cancel := context.WithTimeout(a.tapeContext(context.Background()), 5*time.Second)
	defer cancel()
	state, err := a.tape.Get(ctx)
	if err != nil {
		return err
	}
	a.execution.sessionID = state.SessionID
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
		if err := a.appendAnchor(ctx, anchor.Record{Type: anchor.TapeInitialized, Source: &pb.Source{AgentId: a.principal, TapeId: uuid.New().String()}}); err != nil {
			return err
		}
	}
	if active := a.execution.active; active != nil {
		if err := a.appendAnchor(ctx, anchor.Record{Type: anchor.TurnClosed, TurnID: active.id, StartSeq: active.start.String(), OwnerEpoch: a.execution.epoch, Outcome: "abandoned"}); err != nil {
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
		var ref *pb.TapeView
		if identified {
			ref = anchor.CloneRef(a.execution.latest)
		}
		return nil, &BusyError{view: ref}
	}
	if err := a.appendAnchor(ctx, anchor.Record{Type: anchor.TurnStarted, TurnID: uuid.New().String(), OwnerEpoch: a.execution.epoch, BaseView: anchor.CloneRef(a.execution.latest)}); err != nil {
		return nil, err
	}
	a.execution.cancel = cancel
	a.execution.done = make(chan struct{})
	return a.execution.done, nil
}

func (a *Anra) finish(runErr error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	active := a.execution.active
	if active == nil || a.execution.fenced {
		return ErrFenced
	}
	r := anchor.Record{Type: anchor.TurnClosed, TurnID: active.id, StartSeq: active.start.String(), Outcome: "completed"}
	if runErr != nil {
		r.Outcome = "failed"
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			r.Outcome = "cancelled"
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
