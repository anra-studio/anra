package events

import (
	"encoding/json"
	"fmt"
	"github.com/scbizu/anra/pkg/action"
)

// DecisionType identifies whether a turn continues with actions or completes.
type DecisionType string

const (
	DecisionContinue DecisionType = "continue"
	DecisionComplete DecisionType = "complete"
)

// Decision is the canonical outcome of one agent turn.
type Decision struct {
	Type    DecisionType    `json:"type"`
	Actions []action.Action `json:"actions,omitempty"`
	Output  json.RawMessage `json:"output,omitempty"`
}

func (data Decision) Validate() error {
	switch data.Type {
	case DecisionContinue:
		if len(data.Actions) == 0 || len(data.Output) != 0 {
			return fmt.Errorf("continue requires actions only")
		}
		for index, requested := range data.Actions {
			if requested.ID == "" || requested.Name == "" || !validRawJSON(requested.Args) {
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

// DecodeDecision decodes and validates a turn decision.
func DecodeDecision(data []byte) (Decision, error) { return decodeRecord(data, Decision.Validate) }
