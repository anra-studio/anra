package events

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func decodeRecord[T any](data []byte, validate func(T) error) (T, error) {
	var value T
	if err := decodePayload(data, &value); err != nil {
		return value, err
	}
	if validate != nil {
		if err := validate(value); err != nil {
			return value, err
		}
	}
	return value, nil
}

// DecodeNoPayload validates an empty event body.
func DecodeNoPayload(data []byte) error { _, err := decodeRecord[struct{}](data, nil); return err }
func decodePayload(data json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func validRawJSON(data json.RawMessage) bool {
	return len(data) != 0 && json.Valid(data)
}
