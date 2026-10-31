// Package projection defines the public model representation of Tape entries.
// It owns supported protocol fields and privacy rules, independently of transport.
package projection

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	te "github.com/scbizu/tape-go/pkg/tape/entry"
)

// ErrUnsupported means an entry cannot be faithfully projected by this version.
var ErrUnsupported = errors.New("unsupported entry projection")

// Entry is an entry's public model input. Event holds structured model content;
// when absent, Summary is the plain user/assistant text. For structured events,
// Summary concatenates public text parts and never uses the stored summary.
// Storage identity and transport envelopes remain the caller's responsibility.
type Entry struct {
	Summary string
	Event   *Event
}

// Project removes private execution metadata and thoughts while preserving
// supported model content. Unknown visible protocol fields and legacy runner
// messages fail explicitly. Business args/response JSON remains opaque.
func Project(raw te.EntryLike) (*Entry, error) {
	switch raw.GetKind() {
	case te.EntryUser, te.EntryAssistant, te.EntryToolCall, te.EntryToolResult:
	default:
		return nil, ErrUnsupported
	}
	extensions := extensionsOf(raw)
	if _, legacy := extensions["anra:runner-message/v1"]; legacy {
		return nil, ErrUnsupported
	}
	value, exists := extensions["adk:event"]
	if !exists {
		switch raw.GetKind() {
		case te.EntryUser, te.EntryAssistant:
			return &Entry{Summary: raw.GetSummary()}, nil
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
	return &Entry{Summary: summary, Event: event}, nil
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

func projectEvent(payload string) (*Event, string, error) {
	// Execution metadata outside content is intentionally private and ignored.
	var event struct {
		Content json.RawMessage `json:"content" validate:"required,jsonobject"`
	}
	if err := json.Unmarshal([]byte(payload), &event); err != nil || validate.Struct(event) != nil {
		return nil, "", ErrUnsupported
	}
	var input struct {
		Role  string            `json:"role"`
		Parts []json.RawMessage `json:"parts"`
	}
	if strictJSON(event.Content, &input) != nil {
		return nil, "", ErrUnsupported
	}
	projected := &Event{Role: input.Role}
	var summary strings.Builder
	for _, raw := range input.Parts {
		var visibility struct {
			Thought bool `json:"thought"`
		}
		if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &visibility) != nil {
			return nil, "", ErrUnsupported
		}
		if visibility.Thought {
			continue
		}
		var part struct {
			Part
			Thought          bool            `json:"thought"`
			ThoughtSignature json.RawMessage `json:"thoughtSignature"`
		}
		// DisallowUnknownFields recurses through every protocol object, including
		// response parts and media. It stops at opaque args/response RawMessages.
		if strictJSON(raw, &part) != nil || validate.Struct(part.Part) != nil {
			return nil, "", ErrUnsupported
		}
		if part.Text == "" && part.FunctionCall == nil && part.FunctionResponse == nil {
			continue
		}
		projected.Parts = append(projected.Parts, &part.Part)
		summary.WriteString(part.Text)
	}
	return projected, summary.String(), nil
}

// Input RawMessages have already passed json.Unmarshal, so each contains one
// complete JSON value. No interface{} or float64 participates in projection.
func strictJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}
