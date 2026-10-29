// Package a2a connects Anra peers through standard A2A and the optional
// permissioned tape-view method extension. Local tape ownership never comes
// from request parameters.
package a2a

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

const ExtensionURI = "urn:anra:a2a:tape-view:v1"
const ExtensionHeader = "A2A-Extensions"
const TextMediaType = "text/plain"
const MaxPageSize = 100
const MaxBodyBytes = 1 << 20
const MaxEntryBytes = 8 << 10

const (
	Granted = pb.DecisionStatus_DECISION_STATUS_GRANTED
	Denied  = pb.DecisionStatus_DECISION_STATUS_DENIED
	Pending = pb.DecisionStatus_DECISION_STATUS_PENDING
)

func validRange(r tv.EntryRange, head te.Seq) bool {
	return !r.SeqS.IsZero() && r.SeqS.Cmp(r.SeqE) < 0 && r.SeqE.Cmp(head.Next()) <= 0
}

func decode(data []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidParams, err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return ErrInvalidParams
	}
	return nil
}
