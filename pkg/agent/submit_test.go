package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/scbizu/anra/pkg/agent"
	tapeentry "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage"
	"github.com/scbizu/tape-go/pkg/tape/view"
)

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	store := newStore(t, "p1")
	if _, err := agent.New("", store); err == nil {
		t.Fatal("New() empty principal error = nil")
	}
	if _, err := agent.New("p1", nil); err == nil {
		t.Fatal("New() nil tape error = nil")
	}
}

func TestSubmitPersistsAndDeduplicatesEvent(t *testing.T) {
	store := newStore(t, "p1")
	harness, err := agent.New("p1", store)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	first, err := harness.Submit(context.Background(), agent.Event{
		ID: "e1", Type: agent.EventTypeInputText, Payload: json.RawMessage(` { "text" : "hello", "lang": "en" } `),
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if first.EventID != "e1" || first.Seq != 1 || first.Duplicate {
		t.Fatalf("Submit() = %#v", first)
	}

	// A new Anra proves deduplication is reconstructed from Tape, not cached state.
	restarted, err := agent.New("p1", store)
	if err != nil {
		t.Fatalf("New(restarted) error = %v", err)
	}
	duplicate, err := restarted.Submit(context.Background(), agent.Event{
		ID: "e1", Type: agent.EventTypeInputText, Payload: json.RawMessage(`{"lang":"en","text":"hello"}`),
	})
	if err != nil {
		t.Fatalf("Submit(duplicate) error = %v", err)
	}
	if duplicate.Seq != first.Seq || !duplicate.Duplicate {
		t.Fatalf("Submit(duplicate) = %#v", duplicate)
	}

	projection, err := restarted.Inspect(context.Background())
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if events := projection.Events(); len(events) != 1 || events[0].ID != "e1" {
		t.Fatalf("Inspect().Events() = %#v", events)
	}
}

func TestSubmitRejectsConflictAndInvalidEvents(t *testing.T) {
	store := newStore(t, "p1")
	harness, err := agent.New("p1", store)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	valid := agent.Event{ID: "e1", Type: agent.EventTypeInputText, Payload: json.RawMessage(`{"text":"hello"}`)}
	if _, err := harness.Submit(context.Background(), valid); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if _, err := harness.Submit(context.Background(), agent.Event{ID: "e1", Type: agent.EventTypeInputText, Payload: json.RawMessage(`{"text":"changed"}`)}); !errors.Is(err, agent.ErrEventConflict) {
		t.Fatalf("Submit(conflict) error = %v, want ErrEventConflict", err)
	}
	if _, err := harness.Submit(context.Background(), agent.Event{ID: "e1", Type: "other", Payload: valid.Payload}); !errors.Is(err, agent.ErrEventConflict) {
		t.Fatalf("Submit(type conflict) error = %v, want ErrEventConflict", err)
	}

	tests := []agent.Event{
		{Type: agent.EventTypeInputText, Payload: json.RawMessage(`{}`)},
		{ID: "e2", Payload: json.RawMessage(`{}`)},
		{ID: "e2", Type: "other", Payload: json.RawMessage(`{}`)},
		{ID: "e2", Type: agent.EventTypeInputText},
		{ID: "e2", Type: agent.EventTypeInputText, Payload: json.RawMessage(`{`)},
	}
	for _, event := range tests {
		if _, err := harness.Submit(context.Background(), event); err == nil {
			t.Fatalf("Submit(%#v) error = nil", event)
		}
	}
}

func TestSeparatePrincipalsRemainIsolated(t *testing.T) {
	store := &memoryTape{entries: make(map[string][]tapeentry.EntryLike)}
	for _, principal := range []string{"p1", "p2"} {
		if err := store.Init(owner.WithOwnerId(context.Background(), principal)); err != nil {
			t.Fatalf("Init(%s) error = %v", principal, err)
		}
	}
	one, _ := agent.New("p1", store)
	two, _ := agent.New("p2", store)
	if _, err := one.Submit(context.Background(), agent.Event{ID: "same", Type: agent.EventTypeInputText, Payload: json.RawMessage(`{"text":"one"}`)}); err != nil {
		t.Fatalf("p1 Submit() error = %v", err)
	}
	if _, err := two.Submit(context.Background(), agent.Event{ID: "same", Type: agent.EventTypeInputText, Payload: json.RawMessage(`{"text":"two"}`)}); err != nil {
		t.Fatalf("p2 Submit() error = %v", err)
	}
	p1, _ := one.Inspect(context.Background())
	p2, _ := two.Inspect(context.Background())
	if string(p1.Events()[0].Payload) != `{"text":"one"}` || string(p2.Events()[0].Payload) != `{"text":"two"}` {
		t.Fatalf("principal projections crossed: p1=%s p2=%s", p1.Events()[0].Payload, p2.Events()[0].Payload)
	}
}

func newStore(t *testing.T, principal string) *memoryTape {
	t.Helper()
	store := &memoryTape{entries: make(map[string][]tapeentry.EntryLike)}
	if err := store.Init(owner.WithOwnerId(context.Background(), principal)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	return store
}

type memoryTape struct {
	mu      sync.Mutex
	entries map[string][]tapeentry.EntryLike
}

func (m *memoryTape) Init(ctx context.Context) error {
	principal, err := owner.GetOwnerId(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.entries[principal]; !ok {
		m.entries[principal] = nil
	}
	return nil
}

func (m *memoryTape) Get(ctx context.Context) (view.TapeView, error) {
	principal, entries, err := m.ownerEntries(ctx)
	if err != nil {
		return view.TapeView{}, err
	}
	var head uint64
	if len(entries) > 0 {
		head = entries[len(entries)-1].GetID()
	}
	return view.TapeView{SessionID: "memory", Owner: principal, Scope: view.EntryRange{SeqE: head}}, nil
}

func (m *memoryTape) Store(ctx context.Context, value tapeentry.EntryLike) error {
	principal, err := owner.GetOwnerId(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, ok := m.entries[principal]
	if !ok {
		return fmt.Errorf("memory: principal %q is not initialized", principal)
	}
	if value.GetOwner() != principal {
		return fmt.Errorf("memory: entry owner %q does not match principal %q", value.GetOwner(), principal)
	}
	if value.GetID() == 0 {
		value = value.WithID(uint64(len(entries) + 1))
	}
	m.entries[principal] = append(entries, value)
	return nil
}

func (m *memoryTape) Range(ctx context.Context, scope view.EntryRange, _ ...storage.RangeBy) (view.EntryView, error) {
	principal, entries, err := m.ownerEntries(ctx)
	if err != nil {
		return view.EntryView{}, err
	}
	out := view.EntryView{SessionId: "memory", Owner: principal, Scope: scope}
	for _, value := range entries {
		if value.GetID() >= scope.SeqS && value.GetID() < scope.SeqE {
			out.Raw = append(out.Raw, value)
		}
	}
	return out, nil
}

func (m *memoryTape) Rewind(context.Context, ...storage.RewindBy) (view.EntryRange, error) {
	return view.EntryRange{}, storage.ErrNoAnchor
}

func (m *memoryTape) ownerEntries(ctx context.Context) (string, []tapeentry.EntryLike, error) {
	principal, err := owner.GetOwnerId(ctx)
	if err != nil {
		return "", nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, ok := m.entries[principal]
	if !ok {
		return "", nil, fmt.Errorf("memory: principal %q is not initialized", principal)
	}
	return principal, append([]tapeentry.EntryLike(nil), entries...), nil
}
