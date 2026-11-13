package view

import (
	"encoding/json"
	"fmt"
	"io"
)

// Decode consumes exactly one complete Quote without closing the reader.
// Source errors, truncated JSON and trailing values fail without a partial Quote.
func Decode(source io.Reader) (*Quote, error) {
	decoder := json.NewDecoder(source)
	var quote Quote
	if err := decoder.Decode(&quote); err != nil {
		return nil, fmt.Errorf("decode quote: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("finish quote: %w", err)
		}
		return nil, fmt.Errorf("finish quote: unexpected trailing JSON")
	}
	return &quote, nil
}
