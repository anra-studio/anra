package events

import (
	"encoding/json"
	"fmt"
)

// Event records event data.
type Event struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func (event Event) Validate() error {
	if event.ID == "" {
		return fmt.Errorf("empty event ID")
	}
	if event.Type == "" {
		return fmt.Errorf("empty event type")
	}
	if !validRawJSON(event.Payload) {
		return fmt.Errorf("invalid event payload")
	}
	return nil
}

// DecodeEvent decodes and validates Event data.
func DecodeEvent(data []byte) (Event, error) { return decodeRecord(data, Event.Validate) }
