package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/anra-studio/anra/pkg/model/anchor"
	"github.com/anra-studio/anra/pkg/model/projection"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

var errUnsupportedEntry = &Error{"failed_precondition", "committed view contains an unsupported entry type or projection"}

// ViewEntry exposes only whitelisted model input, or an anchor. It never
// serializes the storage owner, arbitrary extensions, state deltas, or thoughts.
type ViewEntry struct {
	Channel    projection.Channel     `json:"channel"`
	Provenance *projection.Provenance `json:"provenance"`
	Seq        string                 `json:"seq"`
	Kind       string                 `json:"kind"`
	Timestamp  time.Time              `json:"timestamp"`
	Content    any                    `json:"content,omitempty"`
}

type TapeViewResult struct {
	Channel    projection.Channel    `json:"channel"`
	Provenance projection.Provenance `json:"provenance"`
	View       json.RawMessage       `json:"view"`
	Entries    []ViewEntry           `json:"entries"`
}

// readTapeView returns the entries of the granted view. Persisted range
// integrity is the Tape owner's concern, not this service's.
func (s *Service) readTapeView(ctx context.Context, grant *Grant) (*TapeViewResult, error) {
	view := grant.View
	provenance := projection.Provenance{
		Caller: grant.Caller, GrantID: grant.ID, Source: view.Source,
		SnapshotHead: view.SnapshotHead, Scope: view.Scope,
	}
	batch, err := s.config.Tape.Range(s.tapeContext(ctx), tv.EntryRange{
		SeqS: te.MustParseSeq(view.Scope.SeqS),
		SeqE: te.MustParseSeq(view.Scope.SeqE),
	})
	if err != nil {
		return nil, ErrUnavailable
	}
	result := &TapeViewResult{Channel: projection.Borrowed, Provenance: provenance, View: anchor.ProtoJSON(view), Entries: make([]ViewEntry, 0, len(batch.Raw))}
	for _, raw := range batch.Raw {
		e, err := project(raw, &provenance)
		if err != nil {
			return nil, err
		}
		if e != nil {
			result.Entries = append(result.Entries, *e)
		}
	}
	return result, nil
}

func project(raw te.EntryLike, provenance *projection.Provenance) (*ViewEntry, error) {
	e := &ViewEntry{Channel: projection.Borrowed, Provenance: provenance, Seq: raw.GetID().String(), Kind: string(raw.GetKind()), Timestamp: raw.GetTimestamp()}
	if raw.GetKind() == anchor.Kind {
		r, err := anchor.Decode(raw)
		if err != nil {
			return nil, errUnsupportedEntry
		}
		if r == nil {
			e.Content = raw.GetSummary()
			return e, nil
		}
		// The execution owner epoch is private runtime state.
		r.OwnerEpoch = ""
		e.Content = r
		return e, nil
	}
	projected, err := projection.Project(raw, provenance)
	if err != nil {
		return nil, errUnsupportedEntry
	}
	e.Content = projected.Summary
	e.Channel = projected.Channel
	if event := projected.Event; event != nil {
		if len(event.Parts) == 0 {
			return nil, nil // thought-only input has no public model content
		}
		e.Content = event
	}
	return e, nil
}
