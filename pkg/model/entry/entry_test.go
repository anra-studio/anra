package entry_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/anra-studio/anra/pkg/model/entry"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
)

func TestTapeMetadataAndCodecPreserveExtensions(t *testing.T) {
	const payload = `{"content":{"parts":[{"functionCall":{"name":"lookup","args":{"id":9007199254740993}}}]}}`
	original := entry.Entry{}.WithKind(te.EntryAssistant).WithExtension(entry.ADKEvent, payload)
	updated := original.WithExtension("opaque:reference", "retained")
	if _, exists := original.Extension("opaque:reference"); exists {
		t.Fatal("extension update mutated the original entry")
	}
	id := te.MustParseSeq("18446744073709551616")
	timestamp := time.Date(2026, 11, 5, 0, 0, 0, 123, time.UTC)
	stored := updated.WithID(id).WithTimestamp(timestamp)
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	// Tape's persistence codecs restore their own concrete type. Normalize once
	// on entry to Anra and verify that metadata assignment did not erase payloads.
	var legacy te.CustomEntry
	if err := json.Unmarshal(encoded, &legacy); err != nil {
		t.Fatal(err)
	}
	restored, err := entry.FromTape(legacy)
	if err != nil {
		t.Fatal(err)
	}
	value, exists := restored.Extension(entry.ADKEvent)
	if !exists || value != payload || restored.GetID() != id || !restored.GetTimestamp().Equal(timestamp) {
		t.Fatalf("Tape round trip changed entry metadata or event: %s", encoded)
	}
	if value, exists := restored.Extension("opaque:reference"); !exists || value != "retained" {
		t.Fatal("Tape round trip dropped an unknown extension")
	}
}

func TestFromTapeRejectsUnsupportedTypes(t *testing.T) {
	raw := &te.CustomEntry{
		Extensions: map[string]any{
			entry.ADKEvent: "event",
		},
	}
	if _, err := entry.FromTape(raw); err == nil {
		t.Fatal("unsupported entry type must not silently lose extensions")
	}
	if _, err := entry.FromTape(nil); err == nil {
		t.Fatal("nil entry must be rejected")
	}
}

func TestExtensionDecodeDistinguishesAbsentAndUnreadableValues(t *testing.T) {
	var target struct{ Content string }
	if exists, err := (entry.Entry{}).DecodeExtension(entry.ADKEvent, &target); exists || err != nil {
		t.Fatalf("absent extension = %t, %v", exists, err)
	}
	for _, value := range []any{false, "{"} {
		e := entry.Entry{}.WithExtension(entry.ADKEvent, value)
		if exists, err := e.DecodeExtension(entry.ADKEvent, &target); !exists || err == nil {
			t.Fatalf("unreadable extension %v = %t, %v", value, exists, err)
		}
	}
}
