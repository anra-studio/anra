package events

import (
	"fmt"
)

// EffectFailure records effect failure data.
type EffectFailure struct {
	Error string `json:"error"`
}

func (record EffectFailure) Validate() error {
	if record.Error == "" {
		return fmt.Errorf("empty effect error")
	}
	return nil
}

// DecodeEffectFailure decodes and validates EffectFailure data.
func DecodeEffectFailure(data []byte) (EffectFailure, error) {
	return decodeRecord(data, EffectFailure.Validate)
}
