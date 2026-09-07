package events

import (
	"fmt"
	"github.com/scbizu/tape-go/pkg/tape/view"
)

// Activation records activation data.
type Activation struct {
	TriggerEventID string          `json:"trigger_event_id"`
	InputRange     view.EntryRange `json:"input_range"`
}

func (record Activation) Validate() error {
	if record.TriggerEventID == "" {
		return fmt.Errorf("empty trigger event ID")
	}
	if record.InputRange.SeqS > record.InputRange.SeqE {
		return fmt.Errorf("invalid input range")
	}
	return nil
}

// DecodeActivation decodes and validates Activation data.
func DecodeActivation(data []byte) (Activation, error) {
	return decodeRecord(data, Activation.Validate)
}
