package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/scbizu/anra/pkg/lifecycle"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage"
	"github.com/scbizu/tape-go/pkg/tape/view"
)

type Anra struct {
	mu        sync.Mutex
	principal string
	tape      storage.TapeStorage
}

func New(principal string, tape storage.TapeStorage) (*Anra, error) {
	if principal == "" {
		return nil, errors.New("agent: empty principal")
	}
	if tape == nil {
		return nil, errors.New("agent: nil tape")
	}
	return &Anra{principal: principal, tape: tape}, nil
}

func (a *Anra) Inspect(ctx context.Context) (lifecycle.Projection, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inspect(ctx)
}

func (a *Anra) inspect(ctx context.Context) (lifecycle.Projection, error) {
	ctx = owner.WithOwnerId(ctx, a.principal)
	tapeView, err := a.tape.Get(ctx)
	if err != nil {
		return lifecycle.Projection{}, fmt.Errorf("agent: get tape: %w", err)
	}
	if tapeView.Owner != "" && tapeView.Owner != a.principal {
		return lifecycle.Projection{}, fmt.Errorf("agent: tape owner %q does not match principal %q", tapeView.Owner, a.principal)
	}
	if tapeView.Scope.SeqE == math.MaxUint64 {
		return lifecycle.Projection{}, errors.New("agent: tape sequence exhausted")
	}
	entries, err := a.tape.Range(ctx, view.EntryRange{SeqS: 0, SeqE: tapeView.Scope.SeqE + 1})
	if err != nil {
		return lifecycle.Projection{}, fmt.Errorf("agent: read tape: %w", err)
	}
	projection, err := lifecycle.Replay(a.principal, entries.Raw)
	if err != nil {
		return lifecycle.Projection{}, fmt.Errorf("agent: replay tape: %w", err)
	}
	return projection, nil
}
