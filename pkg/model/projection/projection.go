// Package projection routes committed Tape content into semantic channels and
// removes private execution data. Admission and storage own format validation.
package projection

import (
	"encoding/json"
	"errors"
	"strings"

	te "github.com/scbizu/tape-go/pkg/tape/entry"
)

// ErrUnsupported means an entry cannot be faithfully projected by this version.
var ErrUnsupported = errors.New("unsupported entry projection")

// Entry is an entry's public model input. Event holds structured model content;
// when absent, Summary is the plain user/assistant text. For structured events,
// Summary concatenates the authorized text parts, including thoughts, and never
// uses the stored summary. Event retains each part's thought marker.
// Provenance overrides native routing for borrowed content, including tool
// observations; historical roles cannot promote its authority.
type Entry struct {
	Channel    Channel     `json:"channel"`
	Provenance *Provenance `json:"provenance,omitempty"`
	Summary    string      `json:"summary"`
	Event      *Event      `json:"event,omitempty"`
}

// Project removes private execution metadata while preserving authorized model
// content, including thought text and markers. A non-nil provenance routes the
// entry to Borrowed; nil is for native session content. Business args/response
// JSON remains opaque.
// Unknown stored fields are ignored rather than exported or revalidated.
func Project(raw te.EntryLike, provenance *Provenance) (*Entry, error) {
	entry := &Entry{Channel: Primary, Provenance: provenance}
	switch raw.GetKind() {
	case te.EntryUser, te.EntryAssistant:
	case te.EntryToolCall, te.EntryToolResult:
		entry.Channel = Observation
	default:
		return nil, ErrUnsupported
	}
	if provenance != nil {
		entry.Channel = Borrowed
	}
	extensions := extensionsOf(raw)
	value, exists := extensions["adk:event"]
	if !exists {
		switch raw.GetKind() {
		case te.EntryUser, te.EntryAssistant:
			entry.Summary = raw.GetSummary()
			return entry, nil
		default:
			return nil, ErrUnsupported
		}
	}
	payload, ok := value.(string)
	if !ok {
		return nil, ErrUnsupported
	}
	event, summary, err := projectEvent(payload)
	if err != nil {
		return nil, err
	}
	entry.Summary, entry.Event = summary, event
	return entry, nil
}

func extensionsOf(e te.EntryLike) map[string]any {
	switch value := e.(type) {
	case te.CustomEntry:
		return value.Extensions
	case *te.CustomEntry:
		return value.Extensions
	default:
		return nil
	}
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

func projectEvent(payload string) (*Event, string, error) {
	var stored storedEvent
	if json.Unmarshal([]byte(payload), &stored) != nil || stored.Content == nil {
		return nil, "", ErrUnsupported
	}
	projected := &Event{Role: stored.Content.Role}
	var summary strings.Builder
	for _, part := range stored.Content.Parts {
		if part == nil {
			continue
		}
		if part.Text == "" && part.FunctionCall == nil && part.FunctionResponse == nil {
			continue
		}
		projected.Parts = append(projected.Parts, part)
		summary.WriteString(part.Text)
	}
	return projected, summary.String(), nil
}
