// Package entry owns Anra's Tape entry and its extension access. Conversion to
// tape-go's concrete representations belongs at storage and ADK boundaries.
package entry

import (
	"encoding/json"
	"fmt"
	"maps"
	"time"

	te "github.com/scbizu/tape-go/pkg/tape/entry"
)

const (
	ADKEvent      = "adk:event"
	RunnerMessage = "anra:runner-message/v1"
)

// Entry is a value implementing tape-go's EntryLike with private extensions.
// Its zero value is usable. Metadata updates return new values; extension
// updates copy the map. Extension values and imported entries are borrowed
// read-only, so callers must not mutate them while the entry is in use.
type Entry struct {
	base       te.Entry
	extensions map[string]any
}

var _ te.EntryLike = Entry{}

// FromTape normalizes an entry at a foreign storage or SDK boundary. Only this
// package knows the concrete types used by tape-go's persistence codecs.
// raw must be a non-nil entry returned by Tape or the runtime adapter.
func FromTape(raw te.EntryLike) Entry {
	switch value := raw.(type) {
	case Entry:
		return value
	case *Entry:
		return *value
	case te.CustomEntry:
		return Entry{base: value.Entry, extensions: value.Extensions}
	case *te.CustomEntry:
		return Entry{base: value.Entry, extensions: value.Extensions}
	default:
		return Entry{base: te.Entry{
			Seq: raw.GetID(), Ek: raw.GetKind(), Text: raw.GetSummary(),
			Owner: raw.GetOwner(), Timestamp: raw.GetTimestamp(),
		}}
	}
}

func (e Entry) GetID() te.Seq           { return e.base.Seq }
func (e Entry) GetKind() te.EntryKind   { return e.base.Ek }
func (e Entry) GetOwner() string        { return e.base.Owner }
func (e Entry) GetTimestamp() time.Time { return e.base.Timestamp }
func (e Entry) Content() string         { return e.base.Text }
func (e Entry) GetSummary() string      { return e.Content() } // tape-go compatibility

// WithID and WithTimestamp preserve Anra's concrete type and extensions when
// storage assigns metadata. Promoting the embedded tape-go methods would lose them.
func (e Entry) WithID(id te.Seq) te.EntryLike {
	e.base.Seq = id
	return e
}

func (e Entry) WithTimestamp(timestamp time.Time) te.EntryLike {
	e.base.Timestamp = timestamp
	return e
}

func (e Entry) WithKind(kind te.EntryKind) Entry {
	e.base.Ek = kind
	return e
}

func (e Entry) Extension(name string) (any, bool) {
	value, exists := e.extensions[name]
	return value, exists
}

func (e Entry) WithExtension(name string, value any) Entry {
	e.extensions = maps.Clone(e.extensions)
	if e.extensions == nil {
		e.extensions = make(map[string]any)
	}
	e.extensions[name] = value
	return e
}

// DecodeExtension reads a JSON-encoded string extension into the caller's type.
// Missing extensions return false, nil. A present malformed value returns true
// and an error; it must not be mistaken for an absent event or plain text.
func (e Entry) DecodeExtension(name string, target any) (bool, error) {
	value, exists := e.Extension(name)
	if !exists {
		return false, nil
	}
	encoded, ok := value.(string)
	if !ok {
		return true, fmt.Errorf("entry: decode extension %q: expected JSON string, got %T", name, value)
	}
	if err := json.Unmarshal([]byte(encoded), target); err != nil {
		return true, fmt.Errorf("entry: decode extension %q: %w", name, err)
	}
	return true, nil
}

func (e Entry) WithJSONExtension(name string, value any) (Entry, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return Entry{}, fmt.Errorf("entry: encode extension %q: %w", name, err)
	}
	return e.WithExtension(name, string(encoded)), nil
}

// Tape exposes the concrete representation required by tape-go's ADK adapter.
// Its map is borrowed read-only; application code should use Extension instead.
func (e Entry) Tape() te.EntryLike {
	if len(e.extensions) == 0 {
		return e.base
	}
	return te.CustomEntry{Entry: e.base, Extensions: e.extensions}
}

// MarshalJSON keeps the existing Tape storage format, including unknown
// extensions, so bbolt/JSONL recovery does not require rewriting stored records.
func (e Entry) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.Tape())
}
