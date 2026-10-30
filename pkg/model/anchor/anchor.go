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

const Kind te.EntryKind = "anchor:custom"

// Decode returns only native v1 execution records. Valid custom anchors owned
// by other schemas are ignored. Malformed Anra records and invalid v1 records
// fail closed. Add schema-specific decoders at the dispatch boundary as needed.
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
	// Dispatch by exact schema. Other versions and integration records do not
	// belong to the v1 execution state machine, even if they reuse its types.
	switch tag.Schema {
	case Schema:
		return decodeV1(e, raw)
	default:
		return nil, nil
	}
}

func CloneRef(r *pb.TapeView) *pb.TapeView {
	if r == nil {
		return nil
	}
	return proto.Clone(r).(*pb.TapeView)
}
