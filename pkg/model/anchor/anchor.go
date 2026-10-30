// Package anchor defines Anra's versioned, append-only execution records.
package anchor

import (
	"encoding/json"
	"errors"
	"strings"

	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"google.golang.org/protobuf/proto"
)

const Schema = "anra.dev/anchor/v1"
const Kind te.EntryKind = "anchor:custom"

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

// Decode ignores other custom anchors but fails closed for recognized schemas.
func Decode(e te.EntryLike) (*Record, error) {
	if e.GetKind() != Kind {
		return nil, nil
	}
	var tag struct {
		Schema string `json:"schema"`
	}
	raw := []byte(e.GetSummary())
	if err := json.Unmarshal(raw, &tag); err != nil {
		if strings.Contains(string(raw), "anra.dev/anchor/") {
			return nil, errors.New("anchor: malformed record")
		}
		return nil, nil
	}
	if !strings.HasPrefix(tag.Schema, "anra.dev/anchor/") {
		return nil, nil
	}
	if tag.Schema != Schema {
		return nil, errors.New("anchor: unsupported schema")
	}
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

func CloneRef(r *pb.TapeView) *pb.TapeView {
	if r == nil {
		return nil
	}
	return proto.Clone(r).(*pb.TapeView)
}
