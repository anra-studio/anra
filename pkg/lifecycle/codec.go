package lifecycle

import (
	"encoding/json"
	"fmt"

	"github.com/scbizu/tape-go/pkg/tape/entry"
)

const extensionKey = "anra.lifecycle"

// ToCustomEntry stores an immutable lifecycle payload in tape-go's generic
// CustomEntry extension container. The tape-go Entry remains domain-neutral.
func (e Entry) ToCustomEntry(base entry.Entry) (entry.CustomEntry, error) {
	if err := e.Validate(); err != nil {
		return entry.CustomEntry{}, err
	}
	payload, err := json.Marshal(e)
	if err != nil {
		return entry.CustomEntry{}, fmt.Errorf("lifecycle: marshal entry: %w", err)
	}
	return entry.CustomEntry{Entry: base, Extensions: map[string]any{extensionKey: string(payload)}}, nil
}

// FromCustomEntry returns (zero, false, nil) for a non-Anra CustomEntry.
// Corrupt Anra extensions fail closed and are never silently skipped.
func FromCustomEntry(custom entry.CustomEntry) (Entry, bool, error) {
	raw, ok := custom.Extensions[extensionKey]
	if !ok {
		return Entry{}, false, nil
	}
	text, ok := raw.(string)
	if !ok {
		return Entry{}, true, fmt.Errorf("lifecycle: %s extension has type %T", extensionKey, raw)
	}
	var decoded Entry
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		return Entry{}, true, fmt.Errorf("lifecycle: decode extension: %w", err)
	}
	if err := decoded.Validate(); err != nil {
		return Entry{}, true, err
	}
	return decoded, true, nil
}
