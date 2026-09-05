package lifecycle

import (
	"encoding/json"
	"testing"

	"github.com/scbizu/tape-go/pkg/tape/entry"
)

func TestCustomEntryRoundTrip(t *testing.T) {
	want, err := New(EventReceived, EventReceivedData{ID: "e1", Type: "input.text", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	custom, err := want.ToCustomEntry(entry.Entry{Owner: "p1"})
	if err != nil {
		t.Fatalf("ToCustomEntry() error = %v", err)
	}
	got, found, err := FromCustomEntry(custom)
	if err != nil {
		t.Fatalf("FromCustomEntry() error = %v", err)
	}
	if !found || got.Schema != want.Schema || got.Kind != want.Kind || string(got.Data) != string(want.Data) {
		t.Fatalf("FromCustomEntry() = %#v, found %v", got, found)
	}
}

func TestFromCustomEntryRejectsCorruptAnraExtension(t *testing.T) {
	_, found, err := FromCustomEntry(entry.CustomEntry{Extensions: map[string]any{extensionKey: "{"}})
	if !found || err == nil {
		t.Fatalf("FromCustomEntry() found %v, error %v; want corrupt extension error", found, err)
	}
}
