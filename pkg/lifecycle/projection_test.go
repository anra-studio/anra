package lifecycle

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/scbizu/anra/pkg/action"
	tapeentry "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/view"
)

func TestReplayProjectsPendingEventsInSequenceOrder(t *testing.T) {
	entries := []tapeentry.EntryLike{
		plainEntry(1, "p1"),
		lifecycleEntry(t, 2, "p1", EventReceived, EventReceivedData{ID: "e2", Type: "input.text", Payload: json.RawMessage(`{"text":"two"}`)}),
		lifecycleEntry(t, 3, "p1", EventReceived, EventReceivedData{ID: "e1", Type: "input.text", Payload: json.RawMessage(`{"text":"one"}`)}),
	}

	projection, err := Replay("p1", entries)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	pending := projection.PendingEvents()
	if len(pending) != 2 || pending[0].ID != "e2" || pending[0].Seq != 2 || pending[1].ID != "e1" {
		t.Fatalf("PendingEvents() = %#v", pending)
	}
	next, ok := projection.NextPendingEvent()
	if !ok || next.ID != "e2" {
		t.Fatalf("NextPendingEvent() = %#v, %v", next, ok)
	}
}

func TestReplayReconstructsCompletedMultiTurnActivation(t *testing.T) {
	continueDecision := TurnDecidedData{
		Type:    DecisionContinue,
		Actions: []action.Action{{ID: "a1/0/0", Name: "lookup", Args: json.RawMessage(`{"q":"tape"}`)}},
	}
	completeDecision := TurnDecidedData{Type: DecisionComplete, Output: json.RawMessage(`{"text":"done"}`)}
	entries := []tapeentry.EntryLike{
		lifecycleEntry(t, 1, "p1", EventReceived, EventReceivedData{ID: "e1", Type: "input.text", Payload: json.RawMessage(`{"text":"go"}`)}),
		correlatedEntry(t, 2, "p1", ActivationOpened, "a1", nil, nil, ActivationOpenedData{TriggerEventID: "e1", InputRange: view.EntryRange{SeqS: 1, SeqE: 2}}),
		correlatedEntry(t, 3, "p1", TurnOpened, "a1", index(0), nil, EmptyData{}),
		correlatedEntry(t, 4, "p1", TurnDecided, "a1", index(0), nil, continueDecision),
		correlatedEntry(t, 5, "p1", ActionAllowed, "a1", index(0), index(0), AuthorizationData{}),
		correlatedEntry(t, 6, "p1", EffectPrepared, "a1", index(0), index(0), EmptyData{}),
		correlatedEntry(t, 7, "p1", EffectConfirmed, "a1", index(0), index(0), EffectConfirmedData{Result: json.RawMessage(`{"value":"found"}`)}),
		correlatedEntry(t, 8, "p1", TurnOpened, "a1", index(1), nil, EmptyData{}),
		correlatedEntry(t, 9, "p1", TurnDecided, "a1", index(1), nil, completeDecision),
		correlatedEntry(t, 10, "p1", ActivationClosed, "a1", nil, nil, TerminalData{Reason: "completed"}),
	}

	projection, err := Replay("p1", entries)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	activation, ok := projection.Activation("a1")
	if !ok {
		t.Fatal("Activation(a1) not found")
	}
	if activation.Status != ActivationStatusClosed || len(activation.Turns) != 2 {
		t.Fatalf("activation = %#v", activation)
	}
	if len(activation.Turns[0].Actions) != 1 || activation.Turns[0].Actions[0].Effect != EffectStatusConfirmed {
		t.Fatalf("turn 0 = %#v", activation.Turns[0])
	}
	if got := projection.PendingEvents(); len(got) != 0 {
		t.Fatalf("PendingEvents() = %#v, want empty", got)
	}
}

func TestReplayRejectsIllegalTrace(t *testing.T) {
	tests := []struct {
		name    string
		entries []tapeentry.EntryLike
	}{
		{
			name: "duplicate event",
			entries: []tapeentry.EntryLike{
				lifecycleEntry(t, 1, "p1", EventReceived, EventReceivedData{ID: "e1", Type: "input.text", Payload: json.RawMessage(`{"text":"one"}`)}),
				lifecycleEntry(t, 2, "p1", EventReceived, EventReceivedData{ID: "e1", Type: "input.text", Payload: json.RawMessage(`{"text":"one"}`)}),
			},
		},
		{
			name: "foreign principal",
			entries: []tapeentry.EntryLike{
				lifecycleEntry(t, 1, "p2", EventReceived, EventReceivedData{ID: "e1", Type: "input.text", Payload: json.RawMessage(`{"text":"one"}`)}),
			},
		},
		{
			name: "opens event out of fifo order",
			entries: []tapeentry.EntryLike{
				lifecycleEntry(t, 1, "p1", EventReceived, EventReceivedData{ID: "e1", Type: "input.text", Payload: json.RawMessage(`{"text":"one"}`)}),
				lifecycleEntry(t, 2, "p1", EventReceived, EventReceivedData{ID: "e2", Type: "input.text", Payload: json.RawMessage(`{"text":"two"}`)}),
				correlatedEntry(t, 3, "p1", ActivationOpened, "a2", nil, nil, ActivationOpenedData{TriggerEventID: "e2", InputRange: view.EntryRange{SeqS: 1, SeqE: 3}}),
			},
		},
		{
			name: "duplicate turn decision",
			entries: []tapeentry.EntryLike{
				lifecycleEntry(t, 1, "p1", EventReceived, EventReceivedData{ID: "e1", Type: "input.text", Payload: json.RawMessage(`{"text":"one"}`)}),
				correlatedEntry(t, 2, "p1", ActivationOpened, "a1", nil, nil, ActivationOpenedData{TriggerEventID: "e1", InputRange: view.EntryRange{SeqS: 1, SeqE: 2}}),
				correlatedEntry(t, 3, "p1", TurnOpened, "a1", index(0), nil, EmptyData{}),
				correlatedEntry(t, 4, "p1", TurnDecided, "a1", index(0), nil, TurnDecidedData{Type: DecisionComplete, Output: json.RawMessage(`{"text":"one"}`)}),
				correlatedEntry(t, 5, "p1", TurnDecided, "a1", index(0), nil, TurnDecidedData{Type: DecisionComplete, Output: json.RawMessage(`{"text":"two"}`)}),
			},
		},
		{
			name: "effect before prepare",
			entries: []tapeentry.EntryLike{
				lifecycleEntry(t, 1, "p1", EventReceived, EventReceivedData{ID: "e1", Type: "input.text", Payload: json.RawMessage(`{"text":"one"}`)}),
				correlatedEntry(t, 2, "p1", ActivationOpened, "a1", nil, nil, ActivationOpenedData{TriggerEventID: "e1", InputRange: view.EntryRange{SeqS: 1, SeqE: 2}}),
				correlatedEntry(t, 3, "p1", TurnOpened, "a1", index(0), nil, EmptyData{}),
				correlatedEntry(t, 4, "p1", TurnDecided, "a1", index(0), nil, TurnDecidedData{Type: DecisionContinue, Actions: []action.Action{{ID: "a1/0/0", Name: "lookup", Args: json.RawMessage(`{}`)}}}),
				correlatedEntry(t, 5, "p1", ActionAllowed, "a1", index(0), index(0), AuthorizationData{}),
				correlatedEntry(t, 6, "p1", EffectConfirmed, "a1", index(0), index(0), EffectConfirmedData{Result: json.RawMessage(`{}`)}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Replay("p1", tt.entries); err == nil {
				t.Fatal("Replay() error = nil")
			}
		})
	}
}

func TestReplayProjectsBlockedAndFailedActivations(t *testing.T) {
	blocked := []tapeentry.EntryLike{
		lifecycleEntry(t, 1, "p1", EventReceived, EventReceivedData{ID: "e1", Type: "input.text", Payload: json.RawMessage(`{}`)}),
		correlatedEntry(t, 2, "p1", ActivationOpened, "a1", nil, nil, ActivationOpenedData{TriggerEventID: "e1", InputRange: view.EntryRange{SeqS: 1, SeqE: 2}}),
		correlatedEntry(t, 3, "p1", TurnOpened, "a1", index(0), nil, EmptyData{}),
		correlatedEntry(t, 4, "p1", TurnDecided, "a1", index(0), nil, TurnDecidedData{Type: DecisionContinue, Actions: []action.Action{{ID: "a1/0/0", Name: "send", Args: json.RawMessage(`{}`)}}}),
		correlatedEntry(t, 5, "p1", ActionAllowed, "a1", index(0), index(0), AuthorizationData{}),
		correlatedEntry(t, 6, "p1", EffectPrepared, "a1", index(0), index(0), EmptyData{}),
		correlatedEntry(t, 7, "p1", EffectDropped, "a1", index(0), index(0), EffectDroppedData{Reason: "receipt unavailable"}),
		correlatedEntry(t, 8, "p1", ActivationBlocked, "a1", nil, nil, TerminalData{Reason: "effect_dropped"}),
	}
	projection, err := Replay("p1", blocked)
	if err != nil {
		t.Fatalf("Replay(blocked) error = %v", err)
	}
	activation, _ := projection.Activation("a1")
	if activation.Status != ActivationStatusBlocked || activation.Reason != "effect_dropped" {
		t.Fatalf("blocked activation = %#v", activation)
	}

	failed := []tapeentry.EntryLike{
		lifecycleEntry(t, 1, "p1", EventReceived, EventReceivedData{ID: "e1", Type: "input.text", Payload: json.RawMessage(`{}`)}),
		correlatedEntry(t, 2, "p1", ActivationOpened, "a1", nil, nil, ActivationOpenedData{TriggerEventID: "e1", InputRange: view.EntryRange{SeqS: 1, SeqE: 2}}),
		correlatedEntry(t, 3, "p1", ActivationFailed, "a1", nil, nil, TerminalData{Reason: "invalid runner response"}),
	}
	projection, err = Replay("p1", failed)
	if err != nil {
		t.Fatalf("Replay(failed) error = %v", err)
	}
	activation, _ = projection.Activation("a1")
	if activation.Status != ActivationStatusFailed || activation.Reason != "invalid runner response" {
		t.Fatalf("failed activation = %#v", activation)
	}
}

func TestReplayRejectsCorruptAnraEntry(t *testing.T) {
	corrupt := tapeentry.CustomEntry{
		Entry: tapeentry.NewEntry(tapeentry.WithEntryID(1), tapeentry.WithEntryOwner("p1")),
		Extensions: map[string]any{
			extensionKey: "{",
		},
	}
	_, err := Replay("p1", []tapeentry.EntryLike{corrupt})
	if err == nil || !errors.Is(err, ErrInvalidTrace) {
		t.Fatalf("Replay() error = %v, want ErrInvalidTrace", err)
	}
}

func lifecycleEntry(t *testing.T, seq uint64, owner string, kind Kind, data any) tapeentry.EntryLike {
	t.Helper()
	return correlatedEntry(t, seq, owner, kind, "", nil, nil, data)
}

func correlatedEntry(t *testing.T, seq uint64, owner string, kind Kind, activationID string, turnIndex, actionIndex *uint32, data any) tapeentry.EntryLike {
	t.Helper()
	opts := []Option{}
	if activationID != "" {
		opts = append(opts, WithActivationID(activationID))
	}
	if turnIndex != nil {
		opts = append(opts, WithTurnIndex(*turnIndex))
	}
	if actionIndex != nil {
		opts = append(opts, WithActionIndex(*actionIndex))
	}
	lifecycle, err := New(kind, data, opts...)
	if err != nil {
		t.Fatalf("New(%s) error = %v", kind, err)
	}
	custom, err := lifecycle.ToCustomEntry(tapeentry.NewEntry(
		tapeentry.WithEntryID(seq),
		tapeentry.WithEntryKind(tapeentry.EntrySystem),
		tapeentry.WithEntryOwner(owner),
	))
	if err != nil {
		t.Fatalf("ToCustomEntry() error = %v", err)
	}
	return custom
}

func plainEntry(seq uint64, owner string) tapeentry.EntryLike {
	return tapeentry.NewEntry(tapeentry.WithEntryID(seq), tapeentry.WithEntryOwner(owner))
}

func index(value uint32) *uint32 { return &value }
