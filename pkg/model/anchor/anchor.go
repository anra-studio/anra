// Package anchor defines Anra's versioned, append-only execution records.
package anchor

import (
	"encoding/json"
	"errors"
	"fmt"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/anra-studio/anra/pkg/model/entry"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"google.golang.org/protobuf/proto"
)

const Kind te.EntryKind = "anchor:custom"

var ErrUnsupported = errors.New("anchor: unsupported schema")

// Schema is the envelope identifying the format of a custom anchor.
type Schema struct {
	Schema string `json:"schema" validate:"required"`
}

// Decode returns native v1 execution records. Unknown custom anchor schemas
// return ErrUnsupported; malformed envelopes and records return an error.
func Decode(e entry.Entry) (*BuiltIn, error) {
	if e.GetKind() != Kind {
		return nil, nil
	}
	var tag Schema
	raw := []byte(e.Body())
	if err := json.Unmarshal(raw, &tag); err != nil {
		return nil, fmt.Errorf("anchor: decode schema: %w", err)
	}
	if err := recordValidator.Struct(tag); err != nil {
		return nil, fmt.Errorf("anchor: validate schema: %w", err)
	}
	// Dispatch by exact schema. Other versions and integration records do not
	// belong to the v1 execution state machine, even if they reuse its types.
	switch tag.Schema {
	case BuiltInSchema:
		return decodeBuiltIn(e, raw)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupported, tag.Schema)
	}
}

func CloneRef(r *pb.Ref) *pb.Ref {
	if r == nil {
		return nil
	}

	return proto.Clone(r).(*pb.Ref)
}
