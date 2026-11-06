package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"uuid"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/anra-studio/anra/pkg/agent"
	"github.com/anra-studio/anra/pkg/model/anchor"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/proto"
)

// These fixtures commit actual execution records and recover the real Agent's
// view index. They never authorize a range merely because it exists in Tape.
type fixtureTurn struct {
	Source *pb.Source
	ID     string
	Start  te.Seq
}
type recoveredViews struct {
	Tape     storage.TapeStorage
	Owner    string
	All      bool
	Selected *pb.Ref
}

func (v recoveredViews) View(ctx context.Context, caller string, ref *pb.Ref) (result *pb.Ref, err error) {
	a, err := agent.New(v.Owner, v.Tape)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, a.Shutdown(ctx))
	}()
	if v.Selected != nil {
		if !proto.Equal(v.Selected.Source, a.Source()) || (ref != nil && !proto.Equal(ref, v.Selected)) {
			return nil, nil
		}
		return proto.Clone(v.Selected).(*pb.Ref), nil
	}
	if ref == nil && v.All {
		state, e := v.Tape.Get(owner.WithOwnerId(ctx, v.Owner))
		if e != nil {
			return nil, e
		}
		entries, e := v.Tape.Range(
			owner.WithOwnerId(ctx, v.Owner),
			tv.EntryRange{
				SeqS: te.SeqFromUint64(1),
				SeqE: state.Scope.SeqE.Next(),
			},
		)
		if e != nil {
			return nil, e
		}
		ref = &pb.Ref{
			Source: a.Source(),
		}
		for _, raw := range entries.Raw {
			record, e := anchor.Decode(raw)
			if e != nil {
				return nil, e
			}
			if record != nil && record.Type == anchor.TurnClosed && record.Outcome == "completed" {
				ref.Scopes = append(
					ref.Scopes,
					&pb.EntryRange{
						SeqS: te.MustParseSeq(record.StartSeq).Next().String(),
						SeqE: raw.GetID().String(),
					},
				)
			}
		}
		if len(ref.Scopes) == 0 {
			return nil, nil
		}
	}
	return a.View(ctx, caller, ref)
}

func beginFixtureTurn(ctx context.Context, tape storage.TapeStorage, identity string) (fixtureTurn, error) {
	a, err := agent.New(identity, tape)
	if err != nil {
		return fixtureTurn{}, err
	}
	source := a.Source()
	base, viewErr := a.View(ctx, "fixture", nil)
	if err := errors.Join(viewErr, a.Shutdown(ctx)); err != nil {
		return fixtureTurn{}, err
	}
	turn := fixtureTurn{
		Source: source,
		ID:     uuid.New().String(),
	}
	seq, err := storeFixtureAnchor(
		ctx,
		tape,
		identity,
		anchor.Record{
			Schema:     anchor.Schema,
			Type:       anchor.TurnStarted,
			TurnID:     turn.ID,
			OwnerEpoch: "PRIVATE-EPOCH",
			BaseView:   base,
		},
	)
	turn.Start = seq
	return turn, err
}

func finishFixtureTurn(ctx context.Context, tape storage.TapeStorage, identity string, turn fixtureTurn) (*pb.Ref, error) {
	seq, err := storeFixtureAnchor(
		ctx,
		tape,
		identity,
		anchor.Record{
			Schema:     anchor.Schema,
			Type:       anchor.TurnClosed,
			TurnID:     turn.ID,
			StartSeq:   turn.Start.String(),
			Outcome:    "completed",
			OwnerEpoch: "PRIVATE-EPOCH",
		},
	)
	if err != nil {
		return nil, err
	}
	return &pb.Ref{
		Source: turn.Source,
		Scopes: []*pb.EntryRange{{
			SeqS: turn.Start.Next().String(),
			SeqE: seq.String(),
		}},
	}, nil
}

func storeFixtureAnchor(ctx context.Context, tape storage.TapeStorage, identity string, record anchor.Record) (te.Seq, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return te.Seq{}, err
	}
	raw, err := tape.Store(owner.WithOwnerId(ctx, identity), te.NewAnchor(
		te.Seq{},
		identity,
		te.AnchorKindCustom,
		data,
	))
	if err != nil {
		return te.Seq{}, err
	}
	return raw.GetID(), nil
}
