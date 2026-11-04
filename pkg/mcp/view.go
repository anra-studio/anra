package mcp

import (
	"context"
	"encoding/json"
	"time"

	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	"github.com/anra-studio/anra/pkg/model/anchor"
	"github.com/anra-studio/anra/pkg/model/projection"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

var errUnsupportedEntry = &Error{"failed_precondition", "committed view contains an unsupported entry type or projection"}

// ViewEntry exposes only whitelisted model input, or an anchor. It never
// serializes the storage owner, arbitrary extensions, state deltas, or thoughts.
type ViewEntry struct {
	Seq       string    `json:"seq"`
	Kind      string    `json:"kind"`
	Timestamp time.Time `json:"timestamp"`
	Content   any       `json:"content,omitempty"`
}

type TapeViewResult struct {
	View    json.RawMessage `json:"view"`
	Entries []ViewEntry     `json:"entries"`
}

// readTapeView returns the entries of the granted view. Persisted range
// integrity is the Tape owner's concern, not this service's.
func (s *Service) readTapeView(ctx context.Context, view *pb.TapeView) (*TapeViewResult, error) {
	batch, err := s.config.Tape.Range(s.tapeContext(ctx), tv.EntryRange{
		SeqS: te.MustParseSeq(view.Scope.SeqS),
		SeqE: te.MustParseSeq(view.Scope.SeqE),
	})
	if err != nil {
		return nil, ErrUnavailable
	}
	result := &TapeViewResult{View: anchor.ProtoJSON(view), Entries: make([]ViewEntry, 0, len(batch.Raw))}
	for _, raw := range batch.Raw {
		e, err := project(raw)
		if err != nil {
			return nil, err
		}
		if e != nil {
			result.Entries = append(result.Entries, *e)
		}
	}
	return result, nil
}

func project(raw te.EntryLike) (*ViewEntry, error) {
	e := &ViewEntry{Seq: raw.GetID().String(), Kind: string(raw.GetKind()), Timestamp: raw.GetTimestamp()}
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
	projected, err := projection.Project(raw)
	if err != nil {
		return nil, errUnsupportedEntry
	}
	e.Content = projected.Summary
	if event := projected.Event; event != nil {
		if len(event.Parts) == 0 {
			return nil, nil // thought-only input has no public model content
		}
		e.Content = event
	}
	return e, nil
}
