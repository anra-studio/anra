package agent

import (
	"errors"
	"sync"
	"time"

	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
)

type Anra struct {
	mu        sync.Mutex
	principal string
	execution execution
	tape      *bbolt.Bbolt
	runtime   runtimeConfig
}

// New requires an initialized bbolt tape and exclusive runtime ownership of its
// owner/session. The caller drains Shutdown before closing the tape.
func New(principal string, tape *bbolt.Bbolt, opts ...Option) (*Anra, error) {
	if principal == "" {
		return nil, errors.New("agent: empty principal")
	}
	if tape == nil {
		return nil, errors.New("agent: nil tape")
	}
	anra := &Anra{
		principal: principal,
		tape:      tape,
		runtime: runtimeConfig{
			maxTurns: defaultMaxTurns,
			timeout:  2 * time.Minute,
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
	if err := anra.recoverExecution(); err != nil {
		return nil, err
	}
	if err := anra.initialize(); err != nil {
		return nil, err
	}
	return anra, nil
}

// Tape returns the owned tape for the independently served grant reader.
func (a *Anra) Tape() *bbolt.Bbolt { return a.tape }
