package events

import (
	"encoding/json"
	"fmt"
)

// EffectResult records effect result data.
type EffectResult struct {
	Result json.RawMessage `json:"result"`
}

func (record EffectResult) Validate() error {
	if !validRawJSON(record.Result) {
		return fmt.Errorf("invalid effect result")
	}
	return nil
}

// DecodeEffectResult decodes and validates EffectResult data.
func DecodeEffectResult(data []byte) (EffectResult, error) {
	return decodeRecord(data, EffectResult.Validate)
}
