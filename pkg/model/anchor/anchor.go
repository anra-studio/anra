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
const MediaType = "application/vnd.anra.published-context+json;version=1"
const Kind te.EntryKind = "anchor:custom"

// Publication is the public projection. Coverage is provenance, never permission.
type Publication struct {
	Summary    string           `json:"summary"`
	Covers     []*pb.EntryRange `json:"covers"`
	SourceHead string           `json:"sourceHead"`
}
type Record struct {
	Schema          string        `json:"schema"`
	Type            string        `json:"type"`
	Source          *pb.Source    `json:"source,omitempty"`
	InvocationID    string        `json:"invocationId,omitempty"`
	OwnerEpoch      string        `json:"ownerEpoch,omitempty"`
	BasePublication *pb.AnchorRef `json:"basePublication,omitempty"`
	StartSeq        string        `json:"startSeq,omitempty"`
	Outcome         string        `json:"outcome,omitempty"`
	Publication     *Publication  `json:"publication,omitempty"`
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
	if len(r.InvocationID) > 256 || len(r.OwnerEpoch) > 256 {
		return nil, errors.New("anchor: oversized identity")
	}
	switch r.Type {
	case "tape.initialized":
		if r.Source.GetAgentId() == "" || r.Source.GetTapeId() == "" || r.InvocationID != "" || r.Publication != nil || r.BasePublication != nil || r.StartSeq != "" || r.Outcome != "" || r.OwnerEpoch != "" {
			return nil, errors.New("anchor: invalid source")
		}
	case "invocation.started":
		if r.InvocationID == "" || r.OwnerEpoch == "" || r.StartSeq != "" || r.Publication != nil || r.Source != nil || r.Outcome != "" {
			return nil, errors.New("anchor: invalid start")
		}
	case "context.published", "invocation.closed":
		if r.Source != nil || r.BasePublication != nil || (r.Type == "context.published" && (r.Outcome != "" || r.OwnerEpoch != "")) {
			return nil, errors.New("anchor: invalid record fields")
		}
		start, err := te.ParseSeq(r.StartSeq)
		if err != nil || start.IsZero() || start.Cmp(e.GetID()) >= 0 || r.InvocationID == "" {
			return nil, errors.New("anchor: invalid invocation reference")
		}
		if r.Type == "context.published" && r.Publication == nil {
			return nil, errors.New("anchor: missing publication")
		}
		if r.Type == "invocation.closed" {
			switch r.Outcome {
			case "completed", "failed", "cancelled", "abandoned":
			default:
				return nil, errors.New("anchor: invalid outcome")
			}
			if r.Publication != nil && r.Outcome != "completed" {
				return nil, errors.New("anchor: publication on unsuccessful close")
			}
		}
	default:
		return nil, errors.New("anchor: unknown record type")
	}
	if r.Publication != nil {
		if err := ValidatePublication(r.Publication, e.GetID()); err != nil {
			return nil, err
		}
	}
	return &r, nil
}

func ValidatePublication(p *Publication, before te.Seq) error {
	if p == nil || strings.TrimSpace(p.Summary) == "" || len(p.Summary) > 2048 || len(p.Covers) == 0 || len(p.Covers) > 16 {
		return errors.New("anchor: invalid publication")
	}
	h, err := te.ParseSeq(p.SourceHead)
	if err != nil || h.IsZero() || h.Cmp(before) >= 0 {
		return errors.New("anchor: invalid source head")
	}
	var previous te.Seq
	for _, r := range p.Covers {
		s, err := te.ParseSeq(r.GetSeqS())
		if err != nil || s.IsZero() {
			return errors.New("anchor: invalid coverage")
		}
		end, err := te.ParseSeq(r.GetSeqE())
		if err != nil || end.Cmp(s) <= 0 || end.Cmp(h.Next()) > 0 || (!previous.IsZero() && s.Cmp(previous) < 0) {
			return errors.New("anchor: invalid coverage")
		}
		previous = end
	}
	return nil
}
func CloneRef(r *pb.AnchorRef) *pb.AnchorRef {
	if r == nil {
		return nil
	}
	return proto.Clone(r).(*pb.AnchorRef)
}
