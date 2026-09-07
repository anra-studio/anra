package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/scbizu/anra/pkg/events"
	"slices"
	"sort"

	"github.com/scbizu/anra/pkg/action"
	tapeentry "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/view"
)

var ErrInvalidTrace = errors.New("lifecycle: invalid trace")

type ActivationStatus string

const (
	ActivationStatusOpen    ActivationStatus = "open"
	ActivationStatusClosed  ActivationStatus = "closed"
	ActivationStatusBlocked ActivationStatus = "blocked"
	ActivationStatusFailed  ActivationStatus = "failed"
)

type AuthorizationStatus string

const (
	AuthorizationPending AuthorizationStatus = "pending"
	AuthorizationAllowed AuthorizationStatus = "allowed"
	AuthorizationDenied  AuthorizationStatus = "denied"
)

type EffectStatus string

const (
	EffectStatusNone      EffectStatus = "none"
	EffectStatusPrepared  EffectStatus = "prepared"
	EffectStatusConfirmed EffectStatus = "confirmed"
	EffectStatusFailed    EffectStatus = "failed"
	EffectStatusDropped   EffectStatus = "dropped"
)

type EventState struct {
	Seq uint64
	events.Event
	ActivationID string
}

type ActivationState struct {
	Seq            uint64
	ID             string
	TriggerEventID string
	InputRange     view.EntryRange
	Status         ActivationStatus
	Reason         string
	Turns          []TurnState
}

type TurnState struct {
	Seq      uint64
	Index    uint32
	Decision *events.Decision
	Actions  []ActionState
}

type ActionState struct {
	Index         uint32
	Action        action.Action
	Authorization AuthorizationStatus
	Reason        string
	Effect        EffectStatus
	Result        json.RawMessage
	Error         string
}

type Projection struct {
	events      []EventState
	eventByID   map[string]int
	activations []ActivationState
	active      string
}

func Replay(principal string, entries []tapeentry.EntryLike) (Projection, error) {
	if principal == "" {
		return Projection{}, invalidTrace(0, "empty principal")
	}
	ordered := slices.Clone(entries)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].GetID() < ordered[j].GetID() })

	projection := Projection{eventByID: make(map[string]int)}
	var previousSeq uint64
	for _, raw := range ordered {
		seq := raw.GetID()
		if seq == 0 || seq == previousSeq {
			return Projection{}, invalidTrace(seq, "entry sequence is zero or duplicated")
		}
		previousSeq = seq

		lifecycleEntry, err := decodeLifecycleEntry(raw)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return Projection{}, invalidTrace(seq, err.Error())
		}
		if raw.GetOwner() != principal {
			return Projection{}, invalidTrace(seq, fmt.Sprintf("owner %q does not match principal %q", raw.GetOwner(), principal))
		}
		if err := projection.apply(seq, *lifecycleEntry); err != nil {
			return Projection{}, invalidTrace(seq, err.Error())
		}
	}
	return projection, nil
}

func (p Projection) Events() []EventState {
	events := make([]EventState, len(p.events))
	for index, event := range p.events {
		events[index] = cloneEvent(event)
	}
	return events
}

func (p Projection) PendingEvents() []EventState {
	pending := make([]EventState, 0, len(p.events))
	for _, event := range p.events {
		if event.ActivationID == "" {
			pending = append(pending, cloneEvent(event))
		}
	}
	return pending
}

func (p Projection) NextPendingEvent() (EventState, bool) {
	for _, event := range p.events {
		if event.ActivationID == "" {
			return cloneEvent(event), true
		}
	}
	return EventState{}, false
}

func (p Projection) Event(id string) (EventState, bool) {
	index, ok := p.eventByID[id]
	if !ok {
		return EventState{}, false
	}
	return cloneEvent(p.events[index]), true
}

func (p Projection) Activations() []ActivationState {
	activations := make([]ActivationState, len(p.activations))
	for index, activation := range p.activations {
		activations[index] = cloneActivation(activation)
	}
	return activations
}

func (p Projection) Activation(id string) (ActivationState, bool) {
	for _, activation := range p.activations {
		if activation.ID == id {
			return cloneActivation(activation), true
		}
	}
	return ActivationState{}, false
}

func (p Projection) ActiveActivation() (ActivationState, bool) {
	if p.active == "" {
		return ActivationState{}, false
	}
	return p.Activation(p.active)
}

func (p *Projection) apply(seq uint64, entry Entry) error {
	switch entry.Kind {
	case EventReceived:
		return p.applyEvent(seq, entry)
	case ActivationOpened:
		return p.applyActivationOpened(seq, entry)
	case ActivationClosed, ActivationBlocked, ActivationFailed:
		return p.applyActivationTerminal(entry)
	case TurnOpened:
		return p.applyTurnOpened(seq, entry)
	case TurnDecided:
		return p.applyTurnDecided(entry)
	case ActionAllowed, ActionDenied:
		return p.applyAuthorization(entry)
	case EffectPrepared, EffectConfirmed, EffectFailed, EffectDropped:
		return p.applyEffect(entry)
	default:
		return fmt.Errorf("unsupported kind %q", entry.Kind)
	}
}

func (p *Projection) applyEvent(seq uint64, entry Entry) error {
	event, err := entry.decodeEvent()
	if err != nil {
		return err
	}
	if _, exists := p.eventByID[event.ID]; exists {
		return fmt.Errorf("event %q already exists", event.ID)
	}
	p.eventByID[event.ID] = len(p.events)
	event.Payload = slices.Clone(event.Payload)
	p.events = append(p.events, EventState{Seq: seq, Event: event})
	return nil
}

func (p *Projection) applyActivationOpened(seq uint64, entry Entry) error {
	data, err := entry.decodeActivation()
	if err != nil {
		return err
	}
	if p.active != "" {
		return fmt.Errorf("activation %q is still active", p.active)
	}
	if _, exists := p.activation(entry.ActivationID); exists {
		return fmt.Errorf("activation %q already exists", entry.ActivationID)
	}
	eventIndex, exists := p.eventByID[data.TriggerEventID]
	if !exists {
		return fmt.Errorf("trigger event %q does not exist", data.TriggerEventID)
	}
	event := &p.events[eventIndex]
	if event.ActivationID != "" {
		return fmt.Errorf("event %q already opened activation %q", event.ID, event.ActivationID)
	}
	next, ok := p.NextPendingEvent()
	if !ok || next.ID != event.ID {
		return fmt.Errorf("event %q is not the next pending event", event.ID)
	}
	if data.InputRange.SeqE > seq || event.Seq < data.InputRange.SeqS || event.Seq >= data.InputRange.SeqE {
		return fmt.Errorf("input range [%d,%d) does not contain trigger event %d before activation", data.InputRange.SeqS, data.InputRange.SeqE, event.Seq)
	}
	event.ActivationID = entry.ActivationID
	p.activations = append(p.activations, ActivationState{
		Seq: seq, ID: entry.ActivationID, TriggerEventID: event.ID,
		InputRange: data.InputRange, Status: ActivationStatusOpen,
	})
	p.active = entry.ActivationID
	return nil
}

func (p *Projection) applyActivationTerminal(entry Entry) error {
	data, err := entry.decodeReason()
	if err != nil {
		return err
	}
	activation, err := p.activeActivation(entry.ActivationID)
	if err != nil {
		return err
	}
	switch entry.Kind {
	case ActivationClosed:
		if len(activation.Turns) == 0 {
			return fmt.Errorf("activation %q cannot close without a turn", activation.ID)
		}
		decision := activation.Turns[len(activation.Turns)-1].Decision
		if decision == nil || decision.Type != events.DecisionComplete {
			return fmt.Errorf("activation %q cannot close before a complete decision", activation.ID)
		}
		activation.Status = ActivationStatusClosed
	case ActivationBlocked:
		if !activation.hasDroppedEffect() {
			return fmt.Errorf("activation %q cannot block without a dropped effect", activation.ID)
		}
		activation.Status = ActivationStatusBlocked
	case ActivationFailed:
		activation.Status = ActivationStatusFailed
	}
	activation.Reason = data.Reason
	p.active = ""
	return nil
}

func (p *Projection) applyTurnOpened(seq uint64, entry Entry) error {
	if err := entry.decodeNoPayload(); err != nil {
		return err
	}
	activation, err := p.activeActivation(entry.ActivationID)
	if err != nil {
		return err
	}
	want := uint32(len(activation.Turns))
	if *entry.TurnIndex != want {
		return fmt.Errorf("turn index %d is not next index %d", *entry.TurnIndex, want)
	}
	if len(activation.Turns) > 0 {
		previous := &activation.Turns[len(activation.Turns)-1]
		if previous.Decision == nil || previous.Decision.Type != events.DecisionContinue || !allActionsTerminal(previous.Actions) {
			return fmt.Errorf("previous turn is not ready to advance")
		}
	}
	activation.Turns = append(activation.Turns, TurnState{Seq: seq, Index: *entry.TurnIndex})
	return nil
}

func (p *Projection) applyTurnDecided(entry Entry) error {
	data, err := entry.decodeDecision()
	if err != nil {
		return err
	}
	turn, err := p.activeTurn(entry)
	if err != nil {
		return err
	}
	if turn.Decision != nil {
		return fmt.Errorf("turn %d already has a decision", turn.Index)
	}
	decision := cloneDecision(data)
	turn.Decision = &decision
	for index, requested := range data.Actions {
		turn.Actions = append(turn.Actions, ActionState{
			Index: uint32(index), Action: cloneAction(requested),
			Authorization: AuthorizationPending, Effect: EffectStatusNone,
		})
	}
	return nil
}

func (p *Projection) applyAuthorization(entry Entry) error {
	var data events.Reason
	var err error
	if entry.Kind == ActionAllowed {
		err = entry.decodeNoPayload()
	} else {
		data, err = entry.decodeReason()
	}
	if err != nil {
		return err
	}
	actionState, turn, err := p.activeAction(entry)
	if err != nil {
		return err
	}
	if turn.Decision == nil || turn.Decision.Type != events.DecisionContinue {
		return fmt.Errorf("turn %d has no continue decision", turn.Index)
	}
	if actionState.Authorization != AuthorizationPending {
		return fmt.Errorf("action %d is already authorized", actionState.Index)
	}
	for index := range turn.Actions[:actionState.Index] {
		if !actionTerminal(turn.Actions[index]) {
			return fmt.Errorf("previous action %d is not terminal", index)
		}
	}
	if entry.Kind == ActionAllowed {
		actionState.Authorization = AuthorizationAllowed
	} else {
		actionState.Authorization = AuthorizationDenied
		actionState.Reason = data.Reason
	}
	return nil
}

func (p *Projection) applyEffect(entry Entry) error {
	var result events.EffectResult
	var failure events.EffectFailure
	var reason events.Reason
	var err error
	switch entry.Kind {
	case EffectPrepared:
		err = entry.decodeNoPayload()
	case EffectConfirmed:
		result, err = entry.decodeEffectResult()
	case EffectFailed:
		failure, err = entry.decodeEffectFailure()
	case EffectDropped:
		reason, err = entry.decodeReason()
	}
	if err != nil {
		return err
	}

	actionState, _, err := p.activeAction(entry)
	if err != nil {
		return err
	}
	if actionState.Authorization != AuthorizationAllowed {
		return fmt.Errorf("action %d is not allowed", actionState.Index)
	}
	switch entry.Kind {
	case EffectPrepared:
		if actionState.Effect != EffectStatusNone {
			return fmt.Errorf("action %d effect already started", actionState.Index)
		}
		actionState.Effect = EffectStatusPrepared
	case EffectConfirmed:
		if actionState.Effect != EffectStatusPrepared {
			return fmt.Errorf("action %d effect was not prepared", actionState.Index)
		}
		actionState.Effect = EffectStatusConfirmed
		actionState.Result = slices.Clone(result.Result)
	case EffectFailed:
		if actionState.Effect != EffectStatusPrepared {
			return fmt.Errorf("action %d effect was not prepared", actionState.Index)
		}
		actionState.Effect = EffectStatusFailed
		actionState.Error = failure.Error
	case EffectDropped:
		if actionState.Effect != EffectStatusPrepared {
			return fmt.Errorf("action %d effect was not prepared", actionState.Index)
		}
		actionState.Effect = EffectStatusDropped
		actionState.Reason = reason.Reason
	}
	return nil
}

func (p *Projection) activeActivation(id string) (*ActivationState, error) {
	if p.active == "" || p.active != id {
		return nil, fmt.Errorf("activation %q is not active", id)
	}
	activation, ok := p.activation(id)
	if !ok || activation.Status != ActivationStatusOpen {
		return nil, fmt.Errorf("activation %q is not open", id)
	}
	return activation, nil
}

func (p *Projection) activation(id string) (*ActivationState, bool) {
	for index := range p.activations {
		if p.activations[index].ID == id {
			return &p.activations[index], true
		}
	}
	return nil, false
}

func (p *Projection) activeTurn(entry Entry) (*TurnState, error) {
	activation, err := p.activeActivation(entry.ActivationID)
	if err != nil {
		return nil, err
	}
	if int(*entry.TurnIndex) >= len(activation.Turns) {
		return nil, fmt.Errorf("turn %d does not exist", *entry.TurnIndex)
	}
	return &activation.Turns[*entry.TurnIndex], nil
}

func (p *Projection) activeAction(entry Entry) (*ActionState, *TurnState, error) {
	turn, err := p.activeTurn(entry)
	if err != nil {
		return nil, nil, err
	}
	if int(*entry.ActionIndex) >= len(turn.Actions) {
		return nil, nil, fmt.Errorf("action %d does not exist", *entry.ActionIndex)
	}
	return &turn.Actions[*entry.ActionIndex], turn, nil
}

func (a ActivationState) hasDroppedEffect() bool {
	for _, turn := range a.Turns {
		for _, actionState := range turn.Actions {
			if actionState.Effect == EffectStatusDropped {
				return true
			}
		}
	}
	return false
}

func allActionsTerminal(actions []ActionState) bool {
	for _, actionState := range actions {
		if !actionTerminal(actionState) {
			return false
		}
	}
	return true
}

func actionTerminal(actionState ActionState) bool {
	return actionState.Authorization == AuthorizationDenied ||
		actionState.Effect == EffectStatusConfirmed ||
		actionState.Effect == EffectStatusFailed
}

func decodeLifecycleEntry(raw tapeentry.EntryLike) (*Entry, error) {
	switch value := raw.(type) {
	case tapeentry.CustomEntry:
		return FromCustomEntry(value)
	case *tapeentry.CustomEntry:
		return FromCustomEntry(*value)
	default:
		return nil, ErrNotFound
	}
}

func invalidTrace(seq uint64, reason string) error {
	return fmt.Errorf("%w at seq %d: %s", ErrInvalidTrace, seq, reason)
}

func cloneEvent(event EventState) EventState {
	event.Payload = slices.Clone(event.Payload)
	return event
}

func cloneActivation(activation ActivationState) ActivationState {
	turns := activation.Turns
	activation.Turns = make([]TurnState, len(turns))
	for turnIndex, turn := range turns {
		activation.Turns[turnIndex] = turn
		if turn.Decision != nil {
			decision := cloneDecision(*turn.Decision)
			activation.Turns[turnIndex].Decision = &decision
		}
		activation.Turns[turnIndex].Actions = make([]ActionState, len(turn.Actions))
		for actionIndex, actionState := range turn.Actions {
			actionState.Action = cloneAction(actionState.Action)
			actionState.Result = slices.Clone(actionState.Result)
			activation.Turns[turnIndex].Actions[actionIndex] = actionState
		}
	}
	return activation
}

func cloneDecision(decision events.Decision) events.Decision {
	decision.Output = slices.Clone(decision.Output)
	actions := decision.Actions
	decision.Actions = make([]action.Action, len(actions))
	for index, requested := range actions {
		decision.Actions[index] = cloneAction(requested)
	}
	return decision
}

func cloneAction(requested action.Action) action.Action {
	requested.Args = slices.Clone(requested.Args)
	return requested
}
