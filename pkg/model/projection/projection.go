// Package projection routes committed Tape content into semantic channels and
// removes private execution data. Admission and storage own format validation.
package projection

import (
	"errors"

	"github.com/anra-studio/anra/pkg/model/entry"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
)

// ErrUnsupported means an entry cannot be faithfully projected by this version.
var ErrUnsupported = errors.New("unsupported entry projection")

// Entry is an entry's public model input. Content preserves structured parts,
// including thought markers. Plain text entries become a single text part.
// Provenance overrides native routing for borrowed content, including tool
// observations; historical roles cannot promote its authority.
type Entry struct {
	Channel    Channel     `json:"channel"`
	Provenance *Provenance `json:"provenance,omitempty"`
	Content    *Event      `json:"content"`
}

// Project removes private execution metadata while preserving authorized model
// content, including thought text and markers. A non-nil provenance routes the
// entry to Borrowed; nil is for native session content. Business args/response
// JSON remains opaque.
// Unknown stored fields are ignored rather than exported or revalidated.
func Project(raw entry.Entry, provenance *Provenance) (*Entry, error) {
	result := &Entry{Channel: Primary, Provenance: provenance}
	switch raw.GetKind() {
	case te.EntryUser, te.EntryAssistant:
	case te.EntryToolCall, te.EntryToolResult:
		result.Channel = Observation
	default:
		return nil, ErrUnsupported
	}
	if provenance != nil {
		result.Channel = Borrowed
	}
	var stored storedEvent
	exists, err := raw.DecodeExtension(entry.ADKEvent, &stored)
	if err != nil {
		return nil, ErrUnsupported
	}
	if !exists {
		switch raw.GetKind() {
		case te.EntryUser, te.EntryAssistant:
			role := "user"
			if raw.GetKind() == te.EntryAssistant {
				role = "model"
			}
			result.Content = &Event{Role: role, Parts: []*Part{{Text: raw.Content()}}}
			return result, nil
		default:
			return nil, ErrUnsupported
		}
	}
	event, err := projectEvent(stored)
	if err != nil {
		return nil, err
	}
	result.Content = event
	return result, nil
}

// storedEvent reads only content; execution metadata outside it is private
// and ignored.
type storedEvent struct {
	Content *storedContent `json:"content"`
}

type storedContent struct {
	Role  string  `json:"role"`
	Parts []*Part `json:"parts"`
}

func projectEvent(stored storedEvent) (*Event, error) {
	if stored.Content == nil {
		return nil, ErrUnsupported
	}
	projected := &Event{Role: stored.Content.Role}
	for _, part := range stored.Content.Parts {
		if part == nil {
			continue
		}
		if part.Text == "" && part.FunctionCall == nil && part.FunctionResponse == nil {
			continue
		}
		projected.Parts = append(projected.Parts, part)
	}
	return projected, nil
}
