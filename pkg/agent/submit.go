package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/scbizu/anra/pkg/events"
	"io"

	"github.com/scbizu/anra/pkg/lifecycle"
	tapeentry "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
)

const EventTypeInputText = "input.text"

var ErrEventConflict = errors.New("agent: event conflict")

type Event = events.Event

type SubmitResult struct {
	EventID   string
	Seq       uint64
	Duplicate bool
}

type EventConflictError struct {
	EventID string
}

func (e EventConflictError) Error() string {
	return fmt.Sprintf("agent: event %q conflicts with the existing event", e.EventID)
}

func (e EventConflictError) Unwrap() error { return ErrEventConflict }

func (a *Anra) Submit(ctx context.Context, event Event) (SubmitResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	normalized, err := normalizeEvent(event)
	if err != nil {
		return SubmitResult{}, err
	}
	projection, err := a.inspect(ctx)
	if err != nil {
		return SubmitResult{}, err
	}
	if existing, ok := projection.Event(normalized.ID); ok {
		if existing.Type != normalized.Type || !bytes.Equal(existing.Payload, normalized.Payload) {
			return SubmitResult{}, EventConflictError{EventID: normalized.ID}
		}
		return SubmitResult{EventID: existing.ID, Seq: existing.Seq, Duplicate: true}, nil
	}
	if normalized.Type != EventTypeInputText {
		return SubmitResult{}, fmt.Errorf("agent: unsupported event type %q", normalized.Type)
	}

	fact, err := lifecycle.NewEvent(normalized)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("agent: create event fact: %w", err)
	}
	custom, err := fact.ToCustomEntry(tapeentry.NewEntry(
		tapeentry.WithEntryKind(tapeentry.EntrySystem),
		tapeentry.WithEntryOwner(a.principal),
	))
	if err != nil {
		return SubmitResult{}, fmt.Errorf("agent: encode event fact: %w", err)
	}
	tapeCtx := owner.WithOwnerId(ctx, a.principal)
	if err := a.tape.Store(tapeCtx, custom); err != nil {
		return SubmitResult{}, fmt.Errorf("agent: append event: %w", err)
	}
	tapeView, err := a.tape.Get(tapeCtx)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("agent: get appended event sequence: %w", err)
	}
	return SubmitResult{EventID: normalized.ID, Seq: tapeView.Scope.SeqE}, nil
}

func normalizeEvent(event Event) (Event, error) {
	if event.ID == "" {
		return Event{}, errors.New("agent: empty event ID")
	}
	if event.Type == "" {
		return Event{}, errors.New("agent: empty event type")
	}
	payload, err := canonicalJSON(event.Payload)
	if err != nil {
		return Event{}, fmt.Errorf("agent: invalid event payload: %w", err)
	}
	event.Payload = payload
	return event, nil
}

func canonicalJSON(data json.RawMessage) (json.RawMessage, error) {
	if len(data) == 0 {
		return nil, errors.New("empty JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return json.Marshal(value)
}
