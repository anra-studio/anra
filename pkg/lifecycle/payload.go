package lifecycle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/scbizu/anra/pkg/action"
	"github.com/scbizu/tape-go/pkg/tape/view"
)

type EmptyData struct{}

type EventReceivedData struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type ActivationOpenedData struct {
	TriggerEventID string          `json:"trigger_event_id"`
	InputRange     view.EntryRange `json:"input_range"`
}

type TerminalData struct {
	Reason string `json:"reason"`
}

type DecisionType string

const (
	DecisionContinue DecisionType = "continue"
	DecisionComplete DecisionType = "complete"
)

type TurnDecidedData struct {
	Type    DecisionType    `json:"type"`
	Actions []action.Action `json:"actions,omitempty"`
	Output  json.RawMessage `json:"output,omitempty"`
}

type AuthorizationData struct {
	Reason string `json:"reason,omitempty"`
}

type EffectConfirmedData struct {
	Result json.RawMessage `json:"result"`
}

type EffectFailedData struct {
	Error string `json:"error"`
}

type EffectDroppedData struct {
	Reason string `json:"reason"`
}

func (e Entry) validatePayload() error {
	switch e.Kind {
	case EventReceived:
		var data EventReceivedData
		if err := decodePayload(e.Data, &data); err != nil || data.ID == "" || data.Type == "" || !validRawJSON(data.Payload) {
			return payloadError(e.Kind, err)
		}
	case ActivationOpened:
		var data ActivationOpenedData
		if err := decodePayload(e.Data, &data); err != nil || data.TriggerEventID == "" || data.InputRange.SeqS > data.InputRange.SeqE {
			return payloadError(e.Kind, err)
		}
	case ActivationClosed, ActivationBlocked, ActivationFailed:
		var data TerminalData
		if err := decodePayload(e.Data, &data); err != nil || data.Reason == "" {
			return payloadError(e.Kind, err)
		}
	case TurnOpened, EffectPrepared:
		var data EmptyData
		if err := decodePayload(e.Data, &data); err != nil {
			return payloadError(e.Kind, err)
		}
	case TurnDecided:
		var data TurnDecidedData
		if err := decodePayload(e.Data, &data); err != nil {
			return payloadError(e.Kind, err)
		}
		if err := validateDecision(e, data); err != nil {
			return fmt.Errorf("lifecycle: invalid %s payload: %w", e.Kind, err)
		}
	case ActionAllowed:
		var data AuthorizationData
		if err := decodePayload(e.Data, &data); err != nil {
			return payloadError(e.Kind, err)
		}
	case ActionDenied:
		var data AuthorizationData
		if err := decodePayload(e.Data, &data); err != nil || data.Reason == "" {
			return payloadError(e.Kind, err)
		}
	case EffectConfirmed:
		var data EffectConfirmedData
		if err := decodePayload(e.Data, &data); err != nil || !validRawJSON(data.Result) {
			return payloadError(e.Kind, err)
		}
	case EffectFailed:
		var data EffectFailedData
		if err := decodePayload(e.Data, &data); err != nil || data.Error == "" {
			return payloadError(e.Kind, err)
		}
	case EffectDropped:
		var data EffectDroppedData
		if err := decodePayload(e.Data, &data); err != nil || data.Reason == "" {
			return payloadError(e.Kind, err)
		}
	}
	return nil
}

func validateDecision(entry Entry, data TurnDecidedData) error {
	switch data.Type {
	case DecisionContinue:
		if len(data.Actions) == 0 || len(data.Output) != 0 {
			return fmt.Errorf("continue requires actions only")
		}
		for index, requested := range data.Actions {
			wantID := fmt.Sprintf("%s/%d/%d", entry.ActivationID, *entry.TurnIndex, index)
			if requested.ID != wantID || requested.Name == "" || !validRawJSON(requested.Args) {
				return fmt.Errorf("invalid action %d", index)
			}
		}
	case DecisionComplete:
		if len(data.Actions) != 0 || !validRawJSON(data.Output) {
			return fmt.Errorf("complete requires output only")
		}
	default:
		return fmt.Errorf("unsupported decision %q", data.Type)
	}
	return nil
}

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

func payloadError(kind Kind, cause error) error {
	if cause == nil {
		return fmt.Errorf("lifecycle: invalid %s payload", kind)
	}
	return fmt.Errorf("lifecycle: invalid %s payload: %w", kind, cause)
}

func validRawJSON(data json.RawMessage) bool {
	return len(data) != 0 && json.Valid(data)
}
