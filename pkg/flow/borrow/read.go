package borrow

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/anra-studio/anra/pkg/model/entry"
	"github.com/anra-studio/anra/pkg/model/view"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

func (s *Service) quote(ctx context.Context, caller, id string) (*view.Quote, error) {
	lease, err := s.config.Grants.Authorize(ctx, caller, id)
	if err != nil {
		return nil, recordError(err)
	}
	q := &view.Quote{
		Channel: view.Borrowed,
		Provenance: view.Provenance{
			Caller:  lease.Caller,
			LeaseID: lease.Id,
			Ref:     lease.Ref,
		},
		Entries: []view.Entry{},
	}
	ctx = owner.WithOwnerId(ctx, s.config.Tape.OwnerID)
	for _, scope := range lease.Ref.Scopes {
		r := tv.EntryRange{
			SeqS: te.MustParseSeq(scope.SeqS),
			SeqE: te.MustParseSeq(scope.SeqE),
		}
		batch, err := s.config.Tape.Range(ctx, r)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrStorage, err)
		}
		next := r.SeqS
		for _, raw := range batch.Raw {
			value, err := entry.FromTape(raw)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrStorage, err)
			}
			if value.GetOwner() != s.config.Tape.OwnerID || value.GetID() != next || next.Cmp(r.SeqE) >= 0 {
				return nil, ErrStorage
			}
			e, err := s.config.Exporter.Export(value)
			if err != nil {
				return nil, err
			}
			q.Entries = append(q.Entries, e)
			next = next.Next()
		}
		if next != r.SeqE {
			return nil, ErrStorage
		}
	}
	return q, nil
}

// ReadAll authorizes against the current lease and reads every scope atomically
// from the caller's perspective: no partial Quote is returned on a failed range.
func (s *Service) ReadAll(ctx context.Context, caller, id string) (*view.Quote, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.quote(ctx, caller, id)
}

type cursor struct {
	LeaseID string `json:"leaseId"`
	Offset  int    `json:"offset"`
}

// Page carries a read continuation separately from the quoted content.
type Page struct {
	Quote      *view.Quote
	NextCursor string
}

// Read returns whole public entries from one lease, preserving the immutable Ref
// across page boundaries. A continuation cannot select another lease or range.
func (s *Service) Read(ctx context.Context, caller, id, continuation string, size int32) (*Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if size < 0 || size > MaxPageSize || len(continuation) > 1024 {
		return nil, ErrInvalidParams
	}
	q, err := s.quote(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	if size == 0 {
		size = 32
	}
	offset := 0
	if continuation != "" {
		data, err := base64.RawURLEncoding.DecodeString(continuation)
		if err != nil {
			return nil, ErrInvalidParams
		}
		var c cursor
		if json.Unmarshal(data, &c) != nil || c.LeaseID != id || c.Offset < 1 || c.Offset >= len(q.Entries) {
			return nil, ErrInvalidParams
		}
		offset = c.Offset
	}
	end := min(offset+int(size), len(q.Entries))
	page := &Page{
		Quote: q,
	}
	if end < len(q.Entries) {
		data, err := json.Marshal(
			cursor{
				LeaseID: id,
				Offset:  end,
			},
		)
		if err != nil {
			return nil, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	q.Entries = q.Entries[offset:end]
	return page, nil
}
