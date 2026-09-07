package lifecycle

import (
	"fmt"
	"github.com/scbizu/anra/pkg/events"
)

type payloadDecoder func(Entry) error

var payloadDecoders = map[Kind]payloadDecoder{
	EventReceived:     validateWith(Entry.decodeEvent),
	ActivationOpened:  validateWith(Entry.decodeActivation),
	ActivationClosed:  validateWith(Entry.decodeReason),
	ActivationBlocked: validateWith(Entry.decodeReason),
	ActivationFailed:  validateWith(Entry.decodeReason),
	TurnOpened:        Entry.decodeNoPayload,
	TurnDecided:       validateWith(Entry.decodeDecision),
	ActionAllowed:     Entry.decodeNoPayload,
	ActionDenied:      validateWith(Entry.decodeReason),
	EffectPrepared:    Entry.decodeNoPayload,
	EffectConfirmed:   validateWith(Entry.decodeEffectResult),
	EffectFailed:      validateWith(Entry.decodeEffectFailure),
	EffectDropped:     validateWith(Entry.decodeReason),
}

func validateWith[T any](decode func(Entry) (T, error)) payloadDecoder {
	return func(entry Entry) error {
		_, err := decode(entry)
		return err
	}
}

func (e Entry) validatePayload() error {
	decode, ok := payloadDecoders[e.Kind]
	if !ok {
		return fmt.Errorf("lifecycle: unsupported kind %q", e.Kind)
	}
	return decode(e)
}

func (e Entry) decodeEvent() (events.Event, error) { return decodeBody(e, events.DecodeEvent) }
func (e Entry) decodeActivation() (events.Activation, error) {
	return decodeBody(e, events.DecodeActivation)
}
func (e Entry) decodeReason() (events.Reason, error) { return decodeBody(e, events.DecodeReason) }
func (e Entry) decodeEffectResult() (events.EffectResult, error) {
	return decodeBody(e, events.DecodeEffectResult)
}
func (e Entry) decodeEffectFailure() (events.EffectFailure, error) {
	return decodeBody(e, events.DecodeEffectFailure)
}
func (e Entry) decodeNoPayload() error {
	if err := events.DecodeNoPayload(e.Data); err != nil {
		return payloadError(e.Kind, err)
	}
	return nil
}
func (e Entry) decodeDecision() (events.Decision, error) {
	value, err := decodeBody(e, events.DecodeDecision)
	if err != nil {
		return value, err
	}
	if err := validateDecisionCorrelation(e.ActivationID, *e.TurnIndex, value); err != nil {
		return value, payloadError(e.Kind, err)
	}
	return value, nil
}
func decodeBody[T any](e Entry, decode func([]byte) (T, error)) (T, error) {
	value, err := decode(e.Data)
	if err != nil {
		return value, payloadError(e.Kind, err)
	}
	return value, nil
}
func validateDecisionCorrelation(activationID string, turnIndex uint32, data events.Decision) error {
	for index, requested := range data.Actions {
		if requested.ID != fmt.Sprintf("%s/%d/%d", activationID, turnIndex, index) {
			return fmt.Errorf("invalid action %d", index)
		}
	}
	return nil
}
func payloadError(kind Kind, cause error) error {
	if cause == nil {
		return fmt.Errorf("lifecycle: invalid %s payload", kind)
	}
	return fmt.Errorf("lifecycle: invalid %s payload: %w", kind, cause)
}
