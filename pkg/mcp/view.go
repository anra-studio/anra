package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/anra-studio/anra/pkg/model/anchor"
	"github.com/anra-studio/anra/pkg/model/entry"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

var errUnsupportedEntry = &Error{"failed_precondition", "committed view contains an unsupported entry type or projection"}

// ViewEntry exposes only whitelisted model input, or an anchor. It never
// serializes the storage owner, arbitrary extensions, state deltas, or provider
// thought signatures. Authorized thought content retains its thought marker.
type ViewEntry struct {
	Channel    entry.Channel     `json:"channel"`
	Provenance *entry.Provenance `json:"provenance"`
	Seq        string            `json:"seq"`
	Kind       string            `json:"kind"`
	Timestamp  time.Time         `json:"timestamp"`
	Content    any               `json:"content,omitempty"`
}

type TapeViewResult struct {
	Channel    entry.Channel    `json:"channel"`
	Provenance entry.Provenance `json:"provenance"`
	View       json.RawMessage  `json:"view"`
	Entries    []ViewEntry      `json:"entries"`
}

// readTapeView returns the entries of the granted view. Persisted range
// integrity is the Tape owner's concern, not this service's.
func (s *Service) readTapeView(ctx context.Context, grant *Grant) (*TapeViewResult, error) {
	view := grant.View
	provenance := entry.Provenance{
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
	result := &TapeViewResult{Channel: entry.Borrowed, Provenance: provenance, View: anchor.ProtoJSON(view), Entries: make([]ViewEntry, 0, len(batch.Raw))}
	for _, raw := range batch.Raw {
		e, err := exportEntry(entry.FromTape(raw), &provenance)
		if err != nil {
			return nil, err
		}
		if e != nil {
			result.Entries = append(result.Entries, *e)
		}
	}
	return result, nil
}

func exportEntry(raw entry.Entry, provenance *entry.Provenance) (*ViewEntry, error) {
	e := &ViewEntry{Channel: entry.Borrowed, Provenance: provenance, Seq: raw.GetID().String(), Kind: string(raw.GetKind()), Timestamp: raw.GetTimestamp()}
	if raw.GetKind() == anchor.Kind {
		content, err := anchor.Export(raw)
		if err != nil {
			return nil, errUnsupportedEntry
		}
		e.Content = content
		return e, nil
	}
	content, err := raw.Content()
	if err != nil {
		return nil, errUnsupportedEntry
	}
	if len(content.Parts) == 0 {
		return nil, nil // no projected model content remains
	}
	e.Content = content
	return e, nil
}
