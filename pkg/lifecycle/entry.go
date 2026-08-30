package lifecycle

import (
	"encoding/json"
	"fmt"
)

const SchemaV1 = "anra.lifecycle/v1"

// Kind identifies one immutable fact in an Activation trace.
type Kind string

const (
	EventReceived     Kind = "event.received"
	ActivationOpened  Kind = "activation.opened"
	ActivationClosed  Kind = "activation.closed"
	ActivationBlocked Kind = "activation.blocked"
	ActivationFailed  Kind = "activation.failed"
	TurnOpened        Kind = "turn.opened"
	TurnDecided       Kind = "turn.decided"
	ActionAllowed     Kind = "action.allowed"
	ActionDenied      Kind = "action.denied"
	EffectPrepared    Kind = "effect.prepared"
	EffectConfirmed   Kind = "effect.confirmed"
	EffectFailed      Kind = "effect.failed"
	EffectDropped     Kind = "effect.dropped"
)

// Entry is Anra's payload for a tape-go CustomEntry. It is an immutable fact,
// never a mutable aggregate representing an entire activation.
type Entry struct {
	Schema       string          `json:"schema"`
	Kind         Kind            `json:"kind"`
	ActivationID string          `json:"activation_id,omitempty"`
	TurnIndex    *uint32         `json:"turn_index,omitempty"`
	ActionIndex  *uint32         `json:"action_index,omitempty"`
	Data         json.RawMessage `json:"data"`
}

func New(kind Kind, data any) (Entry, error) {
	if !kind.valid() {
		return Entry{}, fmt.Errorf("lifecycle: unsupported kind %q", kind)
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return Entry{}, fmt.Errorf("lifecycle: encode %s payload: %w", kind, err)
	}
	return Entry{Schema: SchemaV1, Kind: kind, Data: payload}, nil
}

func (e Entry) Validate() error {
	if e.Schema != SchemaV1 {
		return fmt.Errorf("lifecycle: unsupported schema %q", e.Schema)
	}
	if !e.Kind.valid() {
		return fmt.Errorf("lifecycle: unsupported kind %q", e.Kind)
	}
	if len(e.Data) == 0 || !json.Valid(e.Data) {
		return fmt.Errorf("lifecycle: invalid %s payload", e.Kind)
	}
	return nil
}

func (k Kind) valid() bool {
	switch k {
	case EventReceived, ActivationOpened, ActivationClosed, ActivationBlocked,
		ActivationFailed, TurnOpened, TurnDecided, ActionAllowed, ActionDenied,
		EffectPrepared, EffectConfirmed, EffectFailed, EffectDropped:
		return true
	default:
		return false
	}
}
