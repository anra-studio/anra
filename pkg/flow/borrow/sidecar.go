package borrow

import (
	"context"
	"fmt"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	bindingKind  te.EntryKind = "anra:lease-source/v1"
	decisionKind te.EntryKind = "anra:lease-decision/v1"
)

// sidecar is the borrowing journal's persistence dependency, not a context Tape.
// It adapts native storage records without constructing te.Tape, exposing views,
// or owning the underlying file. Its positions never address source content.
// All operations run under Journal.mu, including head comparison and append.
type sidecar struct {
	storage storage.TapeStorage
	owner   string
	source  *pb.Source
}

func (s *sidecar) bind(ctx context.Context) error {
	ctx = owner.WithOwnerId(ctx, s.owner)
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := s.storage.Get(ctx)
	if err != nil {
		return fmt.Errorf("%w: read lease sidecar binding: %w", ErrStorage, err)
	}
	if state.Scope.SeqE.IsZero() {
		data, err := protojson.Marshal(s.source)
		if err != nil {
			return fmt.Errorf("%w: encode lease sidecar source: %w", ErrStorage, err)
		}
		_, err = s.storage.Store(
			ctx,
			te.Entry{
				Ek:    bindingKind,
				Owner: s.owner,
				Text:  string(data),
			},
		)
		if err != nil {
			return fmt.Errorf("%w: bind lease sidecar: %w", ErrStorage, err)
		}
		return nil
	}
	batch, err := s.storage.Range(
		ctx,
		tv.EntryRange{
			SeqS: te.SeqFromUint64(1),
			SeqE: te.SeqFromUint64(2),
		},
	)
	if err != nil {
		return fmt.Errorf("%w: read lease sidecar source: %w", ErrStorage, err)
	}
	if len(batch.Raw) != 1 || batch.Raw[0].GetID() != te.SeqFromUint64(1) || batch.Raw[0].GetKind() != bindingKind || batch.Raw[0].GetOwner() != s.owner {
		return fmt.Errorf("%w: missing lease sidecar binding", ErrStorage)
	}
	var source pb.Source
	if err := protojson.Unmarshal([]byte(batch.Raw[0].GetSummary()), &source); err != nil {
		return fmt.Errorf("%w: decode lease sidecar source: %w", ErrStorage, err)
	}
	if !proto.Equal(&source, s.source) {
		return fmt.Errorf("%w: lease sidecar source mismatch", ErrStorage)
	}
	return nil
}

// snapshot returns a complete tail of decision records, excluding the binding.
// Storage metadata is only used to address sidecar records; it is never exposed
// as a source Tape view or passed to context assembly.
func (s *sidecar) snapshot(ctx context.Context, after te.Seq) ([]te.EntryLike, te.Seq, error) {
	if err := ctx.Err(); err != nil {
		return nil, te.Seq{}, err
	}
	ctx = owner.WithOwnerId(ctx, s.owner)
	state, err := s.storage.Get(ctx)
	if err != nil {
		return nil, te.Seq{}, fmt.Errorf("%w: lease sidecar head: %w", ErrStorage, err)
	}
	head := state.Scope.SeqE
	if head.IsZero() || after.Cmp(head) > 0 {
		return nil, te.Seq{}, fmt.Errorf("%w: invalid lease sidecar head", ErrStorage)
	}
	next := after.Next()
	if next.Cmp(te.SeqFromUint64(2)) < 0 {
		next = te.SeqFromUint64(2)
	}
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
	for _, record := range batch.Raw {
		if record.GetID() != next || record.GetKind() != decisionKind || record.GetOwner() != s.owner {
			return nil, te.Seq{}, fmt.Errorf("%w: invalid lease sidecar record at %s", ErrStorage, next)
		}
		next = next.Next()
	}
	if next != head.Next() {
		return nil, te.Seq{}, fmt.Errorf("%w: incomplete lease sidecar", ErrStorage)
	}
	return batch.Raw, head, nil
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
