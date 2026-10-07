package agent

import (
	"errors"
	"fmt"
	"sync"

	"github.com/scbizu/anra/internal/tapestorage"
	"github.com/scbizu/tape-go/pkg/tape/storage"
)

type Anra struct {
	mu        sync.Mutex
	principal string
	tape      storage.TapeStorage
	runtime   runtimeConfig
}

// New requires an initialized bbolt tape. Storage decorators must implement
// storage.Unwrapper and ultimately wrap bbolt. The caller closes the tape.
func New(principal string, tape storage.TapeStorage, opts ...Option) (*Anra, error) {
	if principal == "" {
		return nil, errors.New("agent: empty principal")
	}
	if err := tapestorage.RequireBbolt(tape); err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}
	anra := &Anra{
		principal: principal,
		tape:      tape,
		runtime: runtimeConfig{
			maxTurns:   defaultMaxTurns,
			toolByName: make(map[string]Tool),
		},
	}
	for _, opt := range opts {
		if opt == nil {
			return nil, errors.New("agent: nil option")
		}
		if err := opt(anra); err != nil {
			return nil, err
		}
	}
	return anra, nil
}
