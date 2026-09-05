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

type Option func(*Entry)

func WithActivationID(id string) Option {
	return func(entry *Entry) { entry.ActivationID = id }
}

func WithTurnIndex(index uint32) Option {
	return func(entry *Entry) { entry.TurnIndex = &index }
}

func WithActionIndex(index uint32) Option {
	return func(entry *Entry) { entry.ActionIndex = &index }
}

func New(kind Kind, data any, opts ...Option) (Entry, error) {
	if !kind.valid() {
		return Entry{}, fmt.Errorf("lifecycle: unsupported kind %q", kind)
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return Entry{}, fmt.Errorf("lifecycle: encode %s payload: %w", kind, err)
	}
	entry := Entry{Schema: SchemaV1, Kind: kind, Data: payload}
	for _, opt := range opts {
		if opt != nil {
			opt(&entry)
		}
	}
	if err := entry.Validate(); err != nil {
		return Entry{}, err
	}
	return entry, nil
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
	if err := e.validateCorrelation(); err != nil {
		return err
	}
	return e.validatePayload()
}

func (e Entry) validateCorrelation() error {
	activation := e.ActivationID != ""
	turn := e.TurnIndex != nil
	action := e.ActionIndex != nil

	valid := false
	switch e.Kind {
	case EventReceived:
		valid = !activation && !turn && !action
	case ActivationOpened, ActivationClosed, ActivationBlocked, ActivationFailed:
		valid = activation && !turn && !action
	case TurnOpened, TurnDecided:
		valid = activation && turn && !action
	case ActionAllowed, ActionDenied, EffectPrepared, EffectConfirmed, EffectFailed, EffectDropped:
		valid = activation && turn && action
	}
	if !valid {
		return fmt.Errorf("lifecycle: invalid %s correlation", e.Kind)
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
