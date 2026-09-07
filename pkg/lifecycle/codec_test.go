package lifecycle

import (
	"encoding/json"
	"errors"
	"github.com/scbizu/anra/pkg/events"
	"testing"

	"github.com/scbizu/tape-go/pkg/tape/entry"
)

func TestCustomEntryRoundTrip(t *testing.T) {
	want, err := NewEvent(events.Event{ID: "e1", Type: "input.text", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	custom, err := want.ToCustomEntry(entry.Entry{Owner: "p1"})
	if err != nil {
		t.Fatalf("ToCustomEntry() error = %v", err)
	}
	got, err := FromCustomEntry(custom)
	if err != nil {
		t.Fatalf("FromCustomEntry() error = %v", err)
	}
	if got == nil || got.Schema != want.Schema || got.Kind != want.Kind || string(got.Data) != string(want.Data) {
		t.Fatalf("FromCustomEntry() = %#v", got)
	}
}

func TestFromCustomEntryReturnsNotFoundForNonAnraEntry(t *testing.T) {
	got, err := FromCustomEntry(entry.CustomEntry{})
	if got != nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("FromCustomEntry() = %#v, error %v; want nil, ErrNotFound", got, err)
	}
}

func TestFromCustomEntryRejectsCorruptAnraExtension(t *testing.T) {
	got, err := FromCustomEntry(entry.CustomEntry{Extensions: map[string]any{extensionKey: "{"}})
	if got != nil || err == nil {
		t.Fatalf("FromCustomEntry() = %#v, error %v; want corrupt extension error", got, err)
	}
}
