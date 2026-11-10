package e2e_test

import (
	"context"
	"errors"
	"time"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/anra-studio/anra/pkg/flow/borrow"
	"github.com/anra-studio/anra/pkg/flow/grant"
	"github.com/scbizu/tape-go/pkg/tape"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	"github.com/scbizu/tape-go/pkg/tape/storage/jsonl"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

// grantFixture is an embedding host: source context and authorization sidecar
// have separate storage instances. Only the former is wrapped as a Tape.
type grantFixture struct {
	tape    *tape.Tape
	storage storage.TapeStorage
	source  *pb.Source
	owner   string
	close   func() error
	journal *borrow.Journal
	grants  *grant.Service
}

func openGrantFixture(ctx context.Context, path string, source *pb.Source, identity string, content storage.TapeStorage, now func() time.Time, useJSONL bool) (*grantFixture, error) {
	var store storage.TapeStorage
	// JSONL keeps no persistent open descriptor; individual operations close files.
	closeStore := func() error { return nil }
	if useJSONL {
		value, err := jsonl.NewJSONLStorage("lease/v1", path+".jsonl")
		if err != nil {
			return nil, err
		}
		store = value
	} else {
		value, err := bbolt.NewBboltStorage("lease/v1", path)
		if err != nil {
			return nil, err
		}
		store = value
		closeStore = value.Close
	}
	if err := store.Init(owner.WithOwnerId(ctx, identity)); err != nil {
		return nil, errors.Join(err, closeStore())
	}
	journal, err := borrow.NewJournal(
		ctx,
		borrow.JournalConfig{
			Source:  source,
			Owner:   identity,
			Storage: store,
		},
	)
	if err != nil {
		return nil, errors.Join(err, closeStore())
	}
	handler, err := grant.New(
		grant.Config{
			Source: source,
			Owner:  identity,
			Leases: journal,
			Now:    now,
		},
	)
	if err != nil {
		return nil, errors.Join(err, closeStore())
	}

	registry := tv.NewRegistry()
	if err := registry.Register(handler); err != nil {
		return nil, errors.Join(err, closeStore())
	}
	contextTape := &tape.Tape{
		TapeStorage: content,
		OwnerID:     identity,
		Registry:    registry,
	}

	return &grantFixture{
		tape:    contextTape,
		storage: store,
		source:  source,
		owner:   identity,
		close:   closeStore,
		journal: journal,
		grants:  handler,
	}, nil
}

// decisions inspects physical sidecar records without treating the sidecar as
// a context Tape. Every entry is a borrowing decision or release.
func (f *grantFixture) decisions(ctx context.Context) ([]te.EntryLike, te.Seq, error) {
	ctx = owner.WithOwnerId(ctx, f.owner)
	state, err := f.storage.Get(ctx)
	if err != nil {
		return nil, te.Seq{}, err
	}
	if state.Scope.SeqE.IsZero() {
		return nil, state.Scope.SeqE, nil
	}
	batch, err := f.storage.Range(
		ctx,
		tv.EntryRange{
			SeqS: te.SeqFromUint64(1),
			SeqE: state.Scope.SeqE.Next(),
		},
	)
	return batch.Raw, state.Scope.SeqE, err
}

// newService injects the same journal and authorizer into every service instance.
func (f *grantFixture) newService(c borrow.Config) (*borrow.Service, error) {
	c.Journal = f.journal
	c.Grants = f.grants
	return borrow.New(c)
}
