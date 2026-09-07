package lifecycle

import (
	"encoding/json"
	"github.com/scbizu/anra/pkg/events"
	"testing"

	"github.com/scbizu/anra/pkg/action"
	"github.com/scbizu/tape-go/pkg/tape/view"
)

func TestEntryValidation(t *testing.T) {
	tests := []struct {
		name    string
		entry   Entry
		wantErr bool
	}{
		{name: "valid", entry: Entry{Schema: SchemaV1, Kind: EventReceived, Data: []byte(`{"id":"e1","type":"input.text","payload":{}}`)}},
		{name: "unknown schema", entry: Entry{Schema: "anra.lifecycle/v2", Kind: EventReceived, Data: []byte(`{"id":"e1","type":"input.text","payload":{}}`)}, wantErr: true},
		{name: "unknown kind", entry: Entry{Schema: SchemaV1, Kind: "other", Data: []byte(`{}`)}, wantErr: true},
		{name: "invalid data", entry: Entry{Schema: SchemaV1, Kind: EventReceived, Data: []byte(`{`)}, wantErr: true},
		{name: "missing payload field", entry: Entry{Schema: SchemaV1, Kind: EventReceived, Data: []byte(`{"id":"e1","type":"input.text"}`)}, wantErr: true},
		{name: "unexpected correlation", entry: Entry{Schema: SchemaV1, Kind: EventReceived, ActivationID: "a1", Data: []byte(`{"id":"e1","type":"input.text","payload":{}}`)}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.entry.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConstructors(t *testing.T) {
	tests := []struct {
		name  string
		kind  Kind
		build func() (Entry, error)
	}{
		{name: "event", kind: EventReceived, build: func() (Entry, error) {
			return NewEvent(events.Event{ID: "e1", Type: "input.text", Payload: json.RawMessage(`{}`)})
		}},
		{name: "activation", kind: ActivationOpened, build: func() (Entry, error) {
			return NewActivation("a1", "e1", view.EntryRange{SeqS: 1, SeqE: 2})
		}},
		{name: "turn", kind: TurnOpened, build: func() (Entry, error) {
			return NewTurn("a1", 0)
		}},
		{name: "decision", kind: TurnDecided, build: func() (Entry, error) {
			return NewDecision("a1", 0, events.Decision{
				Type:    events.DecisionContinue,
				Actions: []action.Action{{ID: "a1/0/0", Name: "lookup", Args: json.RawMessage(`{}`)}},
			})
		}},
		{name: "close activation", kind: ActivationClosed, build: func() (Entry, error) {
			return CloseActivation("a1", "completed")
		}},
		{name: "block activation", kind: ActivationBlocked, build: func() (Entry, error) {
			return BlockActivation("a1", "effect dropped")
		}},
		{name: "fail activation", kind: ActivationFailed, build: func() (Entry, error) {
			return FailActivation("a1", "runner failed")
		}},
		{name: "allow action", kind: ActionAllowed, build: func() (Entry, error) {
			return AllowAction("a1", 0, 0)
		}},
		{name: "deny action", kind: ActionDenied, build: func() (Entry, error) {
			return DenyAction("a1", 0, 0, "not allowed")
		}},
		{name: "prepare effect", kind: EffectPrepared, build: func() (Entry, error) {
			return PrepareEffect("a1", 0, 0)
		}},
		{name: "confirm effect", kind: EffectConfirmed, build: func() (Entry, error) {
			return ConfirmEffect("a1", 0, 0, json.RawMessage(`{"ok":true}`))
		}},
		{name: "fail effect", kind: EffectFailed, build: func() (Entry, error) {
			return FailEffect("a1", 0, 0, "timeout")
		}},
		{name: "drop effect", kind: EffectDropped, build: func() (Entry, error) {
			return DropEffect("a1", 0, 0, "receipt unavailable")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.build()
			if err != nil {
				t.Fatalf("constructor error = %v", err)
			}
			if got.Kind != tt.kind {
				t.Fatalf("Kind = %q, want %q", got.Kind, tt.kind)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestConstructorsRejectInvalidPayloads(t *testing.T) {
	tests := []struct {
		name  string
		build func() (Entry, error)
	}{
		{name: "event", build: func() (Entry, error) {
			return NewEvent(events.Event{Type: "input.text", Payload: json.RawMessage(`{}`)})
		}},
		{name: "activation", build: func() (Entry, error) {
			return NewActivation("a1", "", view.EntryRange{})
		}},
		{name: "decision", build: func() (Entry, error) {
			return NewDecision("a1", 0, events.Decision{Type: events.DecisionComplete})
		}},
		{name: "activation reason", build: func() (Entry, error) {
			return CloseActivation("a1", "")
		}},
		{name: "action reason", build: func() (Entry, error) {
			return DenyAction("a1", 0, 0, "")
		}},
		{name: "effect result", build: func() (Entry, error) {
			return ConfirmEffect("a1", 0, 0, json.RawMessage(`{`))
		}},
		{name: "effect error", build: func() (Entry, error) {
			return FailEffect("a1", 0, 0, "")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.build(); err == nil {
				t.Fatal("constructor error = nil")
			}
		})
	}
}
