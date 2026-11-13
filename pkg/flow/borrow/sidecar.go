package borrow

import (
	"context"
	"fmt"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/anra-studio/anra/pkg/model/entry"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

const decisionKind te.EntryKind = "anra:lease-decision/v1"

// sidecar is the borrowing journal's persistence dependency, not a context Tape.
// It adapts native storage records without constructing te.Tape, exposing views,
// or owning the underlying file. Its positions never address source content.
// All operations run under Journal.mu, including head comparison and append.
type sidecar struct {
	storage storage.TapeStorage
	owner   string
	source  *pb.Source
}

// snapshot returns the complete tail of decision records after the given head,
// normalized to Anra entries at the storage boundary.
// Storage metadata is only used to address sidecar records; it is never exposed
// as a source Tape view or passed to context assembly.
func (s *sidecar) snapshot(ctx context.Context, after te.Seq) ([]entry.Entry, te.Seq, error) {
	if err := ctx.Err(); err != nil {
		return nil, te.Seq{}, err
	}
	ctx = owner.WithOwnerId(ctx, s.owner)
	state, err := s.storage.Get(ctx)
	if err != nil {
		return nil, te.Seq{}, fmt.Errorf("%w: lease sidecar head: %w", ErrStorage, err)
	}
	head := state.Scope.SeqE
	if after.Cmp(head) > 0 {
		return nil, te.Seq{}, fmt.Errorf("%w: invalid lease sidecar head", ErrStorage)
	}
	if after == head {
		return nil, head, nil
	}
	next := after.Next()
	batch, err := s.storage.Range(
		ctx,
		tv.EntryRange{
			SeqS: next,
			SeqE: head.Next(),
		},
	)
	if err != nil {
		return nil, te.Seq{}, fmt.Errorf("%w: read lease sidecar: %w", ErrStorage, err)
	}
	records := make([]entry.Entry, len(batch.Raw))
	for i, raw := range batch.Raw {
		record, err := entry.FromTape(raw)
		if err != nil {
			return nil, te.Seq{}, fmt.Errorf("%w: decode lease sidecar entry at %s: %w", ErrStorage, next, err)
		}
		if record.GetID() != next || record.GetKind() != decisionKind || record.GetOwner() != s.owner {
			return nil, te.Seq{}, fmt.Errorf("%w: invalid lease sidecar record at %s", ErrStorage, next)
		}
		records[i] = record
		next = next.Next()
	}
	if next != head.Next() {
		return nil, te.Seq{}, fmt.Errorf("%w: incomplete lease sidecar", ErrStorage)
	}
	return records, head, nil
}

func (s *sidecar) append(ctx context.Context, expected te.Seq, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx = owner.WithOwnerId(ctx, s.owner)
	state, err := s.storage.Get(ctx)
	if err != nil {
		return fmt.Errorf("%w: lease sidecar append head: %w", ErrStorage, err)
	}
	if state.Scope.SeqE != expected {
		return ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err = s.storage.Store(
		ctx,
		te.Entry{
			Ek:    decisionKind,
			Owner: s.owner,
			Text:  string(data),
		},
	)
	if err != nil {
		return fmt.Errorf("%w: append lease decision: %w", ErrStorage, err)
	}
	return nil
}
