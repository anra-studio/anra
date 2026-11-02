package mcp

import (
	"context"
	"encoding/json"
	"time"

	"buf.build/go/protovalidate"
	"github.com/anra-studio/anra/pkg/model/anchor"
	"github.com/anra-studio/anra/pkg/model/projection"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

var errUnsupportedEntry = &Error{"failed_precondition", "committed window contains an unsupported entry type or projection"}

// ProjectedEntry exposes only whitelisted model input or a boundary. It never
// serializes the storage owner, arbitrary extensions, state deltas, or thoughts.
type ProjectedEntry struct {
	Seq       string          `json:"seq"`
	Kind      string          `json:"kind"`
	Timestamp time.Time       `json:"timestamp"`
	Content   json.RawMessage `json:"content,omitempty"`
	Boundary  *Boundary       `json:"boundary,omitempty"`
}

// Boundary preserves turn and integration session relationships without
// exporting private execution ownership metadata or delegated task prompts.
type Boundary struct {
	Schema   string  `json:"schema"`
	Type     string  `json:"type"`
	TurnID   string  `json:"turnId,omitempty"`
	StartSeq string  `json:"startSeq,omitempty"`
	Outcome  string  `json:"outcome,omitempty"`
	Origin   *Origin `json:"origin,omitempty"`
}

type WindowResult struct {
	View       json.RawMessage  `json:"view"`
	Entries    []ProjectedEntry `json:"entries"`
	IsComplete bool             `json:"isComplete"`
}

func (s *Service) window(ctx context.Context, h *Handoff) (*WindowResult, error) {
	r := tv.EntryRange{
		SeqS: te.MustParseSeq(h.Context.View.Scope.SeqS),
		SeqE: te.MustParseSeq(h.Context.View.Scope.SeqE),
	}
	batch, err := s.config.Tape.Range(s.tapeContext(ctx), r)
	if err != nil {
		return nil, ErrUnavailable
	}
	// The excluded snapshot head must also be persisted by the controller.
	boundary, err := s.config.Tape.Range(s.tapeContext(ctx), tv.EntryRange{SeqS: r.SeqE, SeqE: r.SeqE.Next()})
	if err != nil {
		return nil, ErrUnavailable
	}
	if len(boundary.Raw) != 1 || boundary.Raw[0].GetID() != r.SeqE || boundary.Raw[0].GetOwner() != s.config.Owner {
		return nil, ErrFailedPrecondition
	}
	result := &WindowResult{View: anchor.ProtoJSON(h.Context.View), Entries: []ProjectedEntry{}, IsComplete: true}
	calls := make(openToolCalls)
	expected := r.SeqS
	projectedBytes := 0
	for _, raw := range batch.Raw {
		if raw.GetID() != expected || raw.GetOwner() != s.config.Owner {
			return nil, ErrFailedPrecondition
		}
		expected = expected.Next()
		e, err := project(raw, calls)
		if err != nil {
			return nil, err
		}
		if e == nil {
			continue
		}
		data, err := json.Marshal(e)
		if err != nil {
			return nil, ErrFailedPrecondition
		}
		projectedBytes += len(data)
		if projectedBytes > s.config.MaxResponseBytes {
			return nil, ErrFailedPrecondition
		}
		result.Entries = append(result.Entries, *e)
	}
	if expected != r.SeqE || len(calls) != 0 {
		return nil, ErrFailedPrecondition
	}
	return result, nil
}

func project(raw te.EntryLike, calls openToolCalls) (*ProjectedEntry, error) {
	e := &ProjectedEntry{Seq: raw.GetID().String(), Kind: string(raw.GetKind()), Timestamp: raw.GetTimestamp()}
	if raw.GetKind() == anchor.Kind {
		if len(calls) != 0 {
			return nil, ErrFailedPrecondition
		}
		r, err := anchor.Decode(raw)
		if err != nil {
			return nil, ErrFailedPrecondition
		}
		if r != nil {
			e.Boundary = &Boundary{Schema: r.Schema, Type: string(r.Type), TurnID: r.TurnID, StartSeq: r.StartSeq, Outcome: r.Outcome}
			return e, nil
		}
		var h Handoff
		if decode([]byte(raw.GetSummary()), &h) != nil || validate.Struct(h) != nil || protovalidate.Validate(h.Context.View) != nil {
			return nil, errUnsupportedEntry
		}
		e.Boundary = &Boundary{Schema: h.Schema, Type: h.Type, Origin: h.Origin}
		return e, nil
	}
	projected, err := projection.Project(raw)
	if err != nil {
		return nil, errUnsupportedEntry
	}
	var content any = projected.Summary
	if event := projected.Event; event != nil {
		if len(event.Parts) == 0 {
			return nil, nil // thought-only input has no public model content
		}
		for _, part := range event.Parts {
			if err := calls.observe(part); err != nil {
				return nil, err
			}
		}
		content = event
	}
	e.Content, err = json.Marshal(content)
	if err != nil {
		return nil, errUnsupportedEntry
	}
	return e, nil
}

// openToolCalls tracks calls awaiting a terminal response in one window.
// Continuing response chunks keep a call open. Every call must close before
// an anchor or the window end.
type openToolCalls map[string]string

func toolCallKey(id, name string) string {
	if id != "" {
		return "id:" + id
	}
	return "name:" + name
}

func (calls openToolCalls) observe(part *projection.Part) error {
	// The shared projection already validates required tool names.
	if call := part.FunctionCall; call != nil {
		key := toolCallKey(call.ID, call.Name)
		if calls[key] != "" {
			return ErrFailedPrecondition
		}
		calls[key] = call.Name
	}
	if response := part.FunctionResponse; response != nil {
		key := toolCallKey(response.ID, response.Name)
		if calls[key] != response.Name {
			return ErrFailedPrecondition
		}
		if response.WillContinue == nil || !*response.WillContinue {
			delete(calls, key)
		}
	}
	return nil
}
