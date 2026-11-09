package borrow

import (
	"context"
	"fmt"
	"io"

	"github.com/anra-studio/anra/pkg/model/entry"
	"github.com/samber/lo"
	"github.com/scbizu/tape-go/pkg/tape"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

// reader gives each scope its own native Tape read state over shared storage.
// Reading never mutates or closes the host's Tape, and never crosses scope gaps.
func (s *Service) reader(ctx context.Context, scopes []tv.EntryRange) io.Reader {
	store := &readStorage{
		TapeStorage: s.config.Tape.TapeStorage,
		ctx:         owner.WithOwnerId(ctx, s.config.Tape.OwnerID),
		owner:       s.config.Tape.OwnerID,
	}
	readers := lo.Map(
		scopes,
		func(scope tv.EntryRange, _ int) io.Reader {
			r := &tape.Tape{
				TapeStorage: store,
				OwnerID:     store.owner,
			}
			r.SetView(scope)
			return r
		},
	)
	return io.MultiReader(readers...)
}

// Native Tape.Read uses a background context and skips missing entries. Borrowed
// reads must preserve request cancellation and fail on holes instead. This
// adapter binds those requirements to every single-entry Range the reader makes.
type readStorage struct {
	storage.TapeStorage
	ctx   context.Context
	owner string
}

func (s *readStorage) Range(_ context.Context, scope tv.EntryRange, opts ...storage.RangeBy) (tv.EntryView, error) {
	if err := s.ctx.Err(); err != nil {
		return tv.EntryView{}, err
	}
	batch, err := s.TapeStorage.Range(s.ctx, scope, opts...)
	if err != nil {
		// A storage EOF is a failure, distinct from the native reader reaching
		// its configured end. Preserve that distinction for json.Decoder.
		return tv.EntryView{}, fmt.Errorf("%w: read borrowed range [%s,%s): %w", ErrStorage, scope.SeqS, scope.SeqE, err)
	}
	if len(batch.Raw) != 1 || batch.Raw[0].GetID() != scope.SeqS || batch.Raw[0].GetOwner() != s.owner {
		return tv.EntryView{}, fmt.Errorf("%w: incomplete borrowed range [%s,%s): %w", ErrStorage, scope.SeqS, scope.SeqE, io.ErrUnexpectedEOF)
	}
	if _, err := entry.FromTape(batch.Raw[0]); err != nil {
		return tv.EntryView{}, err
	}
	return batch, nil
}
