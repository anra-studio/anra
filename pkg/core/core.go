package core

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

type Core struct {
	mu        sync.Mutex
	principal string
	tape      storage.TapeStorage
}

func New(principal string, tape storage.TapeStorage) (*Core, error) {
	if principal == "" {
		return nil, errors.New("core: empty principal")
	}
	if tape == nil {
		return nil, errors.New("core: nil tape")
	}
	return &Core{principal: principal, tape: tape}, nil
}

func (c *Core) Inspect(ctx context.Context) (lifecycle.Projection, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inspect(ctx)
}

func (c *Core) inspect(ctx context.Context) (lifecycle.Projection, error) {
	ctx = owner.WithOwnerId(ctx, c.principal)
	tapeView, err := c.tape.Get(ctx)
	if err != nil {
		return lifecycle.Projection{}, fmt.Errorf("core: get tape: %w", err)
	}
	if tapeView.Owner != "" && tapeView.Owner != c.principal {
		return lifecycle.Projection{}, fmt.Errorf("core: tape owner %q does not match principal %q", tapeView.Owner, c.principal)
	}
	if tapeView.Scope.SeqE == math.MaxUint64 {
		return lifecycle.Projection{}, errors.New("core: tape sequence exhausted")
	}
	entries, err := c.tape.Range(ctx, view.EntryRange{SeqS: 0, SeqE: tapeView.Scope.SeqE + 1})
	if err != nil {
		return lifecycle.Projection{}, fmt.Errorf("core: read tape: %w", err)
	}
	projection, err := lifecycle.Replay(c.principal, entries.Raw)
	if err != nil {
		return lifecycle.Projection{}, fmt.Errorf("core: replay tape: %w", err)
	}
	return projection, nil
}
