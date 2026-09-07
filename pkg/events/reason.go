package events

import (
	"fmt"
)

// Reason records reason data.
type Reason struct {
	Reason string `json:"reason,omitempty"`
}

func (record Reason) Validate() error {
	if record.Reason == "" {
		return fmt.Errorf("empty reason")
	}
	return nil
}

// DecodeReason decodes and validates Reason data.
func DecodeReason(data []byte) (Reason, error) { return decodeRecord(data, Reason.Validate) }
