package lifecycle

import (
	"encoding/json"
	"fmt"
	"github.com/scbizu/anra/pkg/events"

	"github.com/scbizu/tape-go/pkg/tape/view"
)

// NewEvent records an event received from the caller.
func NewEvent(event events.Event) (Entry, error) {
	if err := event.Validate(); err != nil {
		return Entry{}, payloadError(EventReceived, err)
	}
	return newEntry(EventReceived, event, "", nil, nil)
}

// NewActivation records a new activation for the next pending event.
func NewActivation(id, triggerEventID string, inputRange view.EntryRange) (Entry, error) {
	record := events.Activation{
		TriggerEventID: triggerEventID,
		InputRange:     inputRange,
	}
	if err := record.Validate(); err != nil {
		return Entry{}, payloadError(ActivationOpened, err)
	}
	return newEntry(ActivationOpened, record, id, nil, nil)
}

// NewTurn records a new turn in an active activation.
func NewTurn(activationID string, index uint32) (Entry, error) {
	return newEntry(TurnOpened, struct{}{}, activationID, &index, nil)
}

// NewDecision records the decision produced for a turn.
func NewDecision(activationID string, turnIndex uint32, decision events.Decision) (Entry, error) {
	if err := decision.Validate(); err != nil {
		return Entry{}, payloadError(TurnDecided, err)
	}
	if err := validateDecisionCorrelation(activationID, turnIndex, decision); err != nil {
		return Entry{}, payloadError(TurnDecided, err)
	}
	return newEntry(TurnDecided, decision, activationID, &turnIndex, nil)
}

// CloseActivation records a successfully completed activation.
func CloseActivation(id, reason string) (Entry, error) {
	if err := (events.Reason{Reason: reason}).Validate(); err != nil {
		return Entry{}, payloadError(ActivationClosed, err)
	}
	return newEntry(ActivationClosed, events.Reason{Reason: reason}, id, nil, nil)
}

// BlockActivation records an activation blocked on an unrecoverable effect.
func BlockActivation(id, reason string) (Entry, error) {
	if err := (events.Reason{Reason: reason}).Validate(); err != nil {
		return Entry{}, payloadError(ActivationBlocked, err)
	}
	return newEntry(ActivationBlocked, events.Reason{Reason: reason}, id, nil, nil)
}

// FailActivation records an activation terminated by a failure.
func FailActivation(id, reason string) (Entry, error) {
	if err := (events.Reason{Reason: reason}).Validate(); err != nil {
		return Entry{}, payloadError(ActivationFailed, err)
	}
	return newEntry(ActivationFailed, events.Reason{Reason: reason}, id, nil, nil)
}

// AllowAction records authorization to execute an action.
func AllowAction(activationID string, turnIndex, actionIndex uint32) (Entry, error) {
	return newEntry(ActionAllowed, struct{}{}, activationID, &turnIndex, &actionIndex)
}

// DenyAction records denial of an action and its reason.
func DenyAction(activationID string, turnIndex, actionIndex uint32, reason string) (Entry, error) {
	if err := (events.Reason{Reason: reason}).Validate(); err != nil {
		return Entry{}, payloadError(ActionDenied, err)
	}
	return newEntry(ActionDenied, events.Reason{Reason: reason}, activationID, &turnIndex, &actionIndex)
}

// PrepareEffect records the start of an authorized action's effect.
func PrepareEffect(activationID string, turnIndex, actionIndex uint32) (Entry, error) {
	return newEntry(EffectPrepared, struct{}{}, activationID, &turnIndex, &actionIndex)
}

// ConfirmEffect records the successful result of a prepared effect.
func ConfirmEffect(activationID string, turnIndex, actionIndex uint32, result json.RawMessage) (Entry, error) {
	record := events.EffectResult{Result: result}
	if err := record.Validate(); err != nil {
		return Entry{}, payloadError(EffectConfirmed, err)
	}
	return newEntry(EffectConfirmed, record, activationID, &turnIndex, &actionIndex)
}

// FailEffect records the error returned by a prepared effect.
func FailEffect(activationID string, turnIndex, actionIndex uint32, cause string) (Entry, error) {
	record := events.EffectFailure{Error: cause}
	if err := record.Validate(); err != nil {
		return Entry{}, payloadError(EffectFailed, err)
	}
	return newEntry(EffectFailed, record, activationID, &turnIndex, &actionIndex)
}

// DropEffect records that a prepared effect cannot be safely recovered.
func DropEffect(activationID string, turnIndex, actionIndex uint32, reason string) (Entry, error) {
	if err := (events.Reason{Reason: reason}).Validate(); err != nil {
		return Entry{}, payloadError(EffectDropped, err)
	}
	return newEntry(EffectDropped, events.Reason{Reason: reason}, activationID, &turnIndex, &actionIndex)
}

func newEntry(kind Kind, data any, activationID string, turnIndex, actionIndex *uint32) (Entry, error) {
	if !kind.valid() {
		return Entry{}, fmt.Errorf("lifecycle: unsupported kind %q", kind)
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return Entry{}, fmt.Errorf("lifecycle: encode %s payload: %w", kind, err)
	}
	entry := Entry{
		Schema:       SchemaV1,
		Kind:         kind,
		ActivationID: activationID,
		TurnIndex:    turnIndex,
		ActionIndex:  actionIndex,
		Data:         payload,
	}
	if err := entry.validateEnvelope(); err != nil {
		return Entry{}, err
	}
	return entry, nil
}
