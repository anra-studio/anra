package action

import "encoding/json"

// Spec is a fixed V0 definition made visible to the runner for one action.
type Spec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

// Action is one concrete request made by a turn decision.
type Action struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// Result is the structured outcome supplied to a subsequent turn.
type Result struct {
	ActionID string          `json:"action_id"`
	Data     json.RawMessage `json:"data"`
	Err      string          `json:"error,omitempty"`
}
