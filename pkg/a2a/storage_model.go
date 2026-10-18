package a2a

import (
	"encoding/json"
	"time"

	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

// Only tape-go's numeric ranges need conversion at the RPC boundary.
func rangeToProto(r tv.EntryRange) *pb.EntryRange {
	return &pb.EntryRange{SeqS: r.SeqS.String(), SeqE: r.SeqE.String()}
}
func rangeFromProto(r *pb.EntryRange) (tv.EntryRange, error) {
	if r == nil {
		return tv.EntryRange{}, ErrInvalidParams
	}
	s, err := te.ParseSeq(r.SeqS)
	if err != nil {
		return tv.EntryRange{}, ErrInvalidParams
	}
	e, err := te.ParseSeq(r.SeqE)
	if err != nil {
		return tv.EntryRange{}, ErrInvalidParams
	}
	return tv.EntryRange{SeqS: s, SeqE: e}, nil
}

type storedPayload struct {
	MediaType string          `json:"mediaType"`
	Data      json.RawMessage `json:"data"`
}

// storedEntry is the JSON representation that bounds an exported entry's size.
// It contains no owner identity or CustomEntry extensions.
type storedEntry struct {
	tv.ProjectedEntry
	Timestamp time.Time      `json:"timestamp"`
	Payload   *storedPayload `json:"payload,omitempty"`
}

func storeEntry(e *pb.ExportedEntry) (storedEntry, error) {
	seq, err := te.ParseSeq(e.GetSeq())
	if err != nil {
		return storedEntry{}, err
	}
	if e.GetTimestamp() == nil || e.Timestamp.CheckValid() != nil {
		return storedEntry{}, ErrInvalidParams
	}
	out := storedEntry{ProjectedEntry: tv.ProjectedEntry{Seq: seq, Kind: te.EntryKind(e.Kind), Summary: e.Summary}, Timestamp: e.Timestamp.AsTime()}
	if e.Payload != nil {
		out.Payload = &storedPayload{MediaType: e.Payload.MediaType, Data: e.Payload.JsonData}
	}
	return out, nil
}
