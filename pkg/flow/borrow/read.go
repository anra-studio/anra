package borrow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/anra-studio/anra/pkg/model/entry"
	"github.com/anra-studio/anra/pkg/model/view"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
)

// Read authorizes one lease and opens a lazy JSON Quote stream. Only public
// content is emitted, in the lease's complete scope order. The caller must keep
// ctx alive while reading and finish before the host closes storage. A later
// release prevents new readers; an already-authorized reader may finish.
// The stream owns no storage or goroutines. It reports cancellation and missing
// entries as read errors; consumers must not accept an incomplete Quote.
func (s *Service) Read(ctx context.Context, caller, id string) (io.Reader, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, err := s.config.Grants.Authorize(ctx, caller, id)
	if err != nil {
		return nil, recordError(err)
	}
	scopes, err := leaseScopes(lease.Ref)
	if err != nil {
		return nil, err
	}
	provenance, err := json.Marshal(
		view.Provenance{
			Caller:  lease.Caller,
			LeaseID: lease.Id,
			Ref:     lease.Ref,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("%w: encode provenance: %w", ErrStorage, err)
	}
	decoder := json.NewDecoder(s.reader(ctx, scopes))
	decoder.UseNumber()
	entries := &entryStream{
		decoder:  decoder,
		exporter: s.config.Exporter,
	}
	// The fixed framing keeps the Quote open until all entries reach EOF.
	// A source error never produces a closing array/object or a complete Quote.
	header := `{"channel":"` + string(view.Borrowed) + `","provenance":` + string(provenance) + `,"entries":[`
	return &readStream{
		ctx:    ctx,
		mu:     &s.mu,
		source: io.MultiReader(strings.NewReader(header), entries, strings.NewReader(`]}`)),
	}, nil
}

// readStream serializes exporter callbacks with other service operations without
// holding the service lock while a caller is idle. Read errors are terminal.
type readStream struct {
	ctx    context.Context
	mu     *sync.Mutex
	source io.Reader
	err    error
}

func (r *readStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return 0, r.err
	}
	if err := r.ctx.Err(); err != nil {
		r.err = err
		return 0, err
	}
	n, err := r.source.Read(p)
	if canceled := r.ctx.Err(); canceled != nil {
		err = canceled
	}
	r.err = err
	return n, err
}

// entryStream exports one native entry at a time. Its bytes never include raw
// extensions, execution state or provider signatures.
type entryStream struct {
	decoder   *json.Decoder
	exporter  Exporter
	pending   io.Reader
	separator string
}

func (r *entryStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if r.pending != nil {
			n, err := r.pending.Read(p)
			if err != io.EOF {
				return n, err
			}
			r.pending = nil
			if n > 0 {
				return n, nil
			}
		}
		var raw te.CustomEntry
		if err := r.decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				return 0, io.EOF
			}
			return 0, fmt.Errorf("%w: decode borrowed entry: %w", ErrStorage, err)
		}
		value, err := entry.FromTape(raw)
		if err != nil {
			return 0, fmt.Errorf("%w: borrowed entry: %w", ErrStorage, err)
		}
		exported, err := r.exporter.Export(value)
		if err != nil {
			return 0, err
		}
		data, err := json.Marshal(exported)
		if err != nil {
			return 0, fmt.Errorf("%w: encode borrowed entry: %w", view.ErrUnsupported, err)
		}
		r.pending = io.MultiReader(strings.NewReader(r.separator), bytes.NewReader(data))
		r.separator = ","
	}
}
