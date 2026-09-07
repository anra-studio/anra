package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/scbizu/tape-go/pkg/tape/entry"
)

const extensionKey = "anra.lifecycle"

// ErrNotFound indicates that a CustomEntry has no Anra lifecycle extension.
var ErrNotFound = errors.New("lifecycle: entry not found")

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

// FromCustomEntry returns (nil, ErrNotFound) for a non-Anra CustomEntry. It
// validates the lifecycle envelope; Validate or Replay decodes the payload.
func FromCustomEntry(custom entry.CustomEntry) (*Entry, error) {
	raw, ok := custom.Extensions[extensionKey]
	if !ok {
		return nil, ErrNotFound
	}
	text, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("lifecycle: %s extension has type %T", extensionKey, raw)
	}
	var decoded Entry
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		return nil, fmt.Errorf("lifecycle: decode extension: %w", err)
	}
	if err := decoded.validateEnvelope(); err != nil {
		return nil, err
	}
	return &decoded, nil
}
