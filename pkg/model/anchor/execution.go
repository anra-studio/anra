package anchor

import (
	"encoding/json"
	"errors"

	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
)

// Schema identifies native execution records in the custom anchor envelope.
const Schema = "anra.dev/anchor/v1"

// Record describes tape initialization or an execution turn transition.
type Record struct {
	Schema     string       `json:"schema"`
	Type       string       `json:"type"`
	Source     *pb.Source   `json:"source,omitempty"`
	TurnID     string       `json:"turnId,omitempty"`
	OwnerEpoch string       `json:"ownerEpoch,omitempty"`
	BaseView   *pb.TapeView `json:"baseView,omitempty"`
	StartSeq   string       `json:"startSeq,omitempty"`
	Outcome    string       `json:"outcome,omitempty"`
}

// decodeExecutionRecord validates the fields and references of an execution transition.
func decodeExecutionRecord(e te.EntryLike, raw []byte) (*Record, error) {
	var r Record
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	if len(r.TurnID) > 256 || len(r.OwnerEpoch) > 256 {
		return nil, errors.New("anchor: oversized identity")
	}
	switch r.Type {
	case "tape.initialized":
		if r.Source.GetAgentId() == "" || r.Source.GetTapeId() == "" || r.TurnID != "" || r.BaseView != nil || r.StartSeq != "" || r.Outcome != "" || r.OwnerEpoch != "" {
			return nil, errors.New("anchor: invalid source")
		}
	case "turn.started":
		if r.TurnID == "" || r.OwnerEpoch == "" || r.StartSeq != "" || r.Source != nil || r.Outcome != "" {
			return nil, errors.New("anchor: invalid start")
		}
	case "turn.closed":
		if r.Source != nil || r.BaseView != nil {
			return nil, errors.New("anchor: invalid record fields")
		}
		start, err := te.ParseSeq(r.StartSeq)
		if err != nil || start.IsZero() || start.Cmp(e.GetID()) >= 0 || r.TurnID == "" {
			return nil, errors.New("anchor: invalid turn reference")
		}
		switch r.Outcome {
		case "completed", "failed", "cancelled", "abandoned":
		default:
			return nil, errors.New("anchor: invalid outcome")
		}
		if r.Outcome == "completed" && start.Next().Cmp(e.GetID()) >= 0 {
			return nil, errors.New("anchor: empty completed context")
		}
	default:
		return nil, errors.New("anchor: unknown record type")
	}
	return &r, nil
}
