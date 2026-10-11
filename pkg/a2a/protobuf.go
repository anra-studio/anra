package a2a

import (
	"encoding/json"
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

// Only tape-go's numeric ranges need conversion at the storage boundary.
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

// Validate peer responses without constructing a second representation.
func validateDecision(d *pb.Decision, id string) error {
	if d == nil || d.RequestId != id {
		return ErrInvalidParams
	}
	switch d.Status {
	case Granted:
		v := d.View
		if v == nil || v.ViewId == "" || v.GetSource().GetAgentId() == "" || v.GetSource().GetTapeId() == "" || len(v.Ranges) > 1 || v.ExpiresAt == nil || v.ExpiresAt.CheckValid() != nil {
			return ErrInvalidParams
		}
		head, err := te.ParseSeq(v.SnapshotHead)
		if err != nil {
			return ErrInvalidParams
		}
		for _, r := range v.Ranges {
			b, err := rangeFromProto(r)
			if err != nil || !validRange(b, head) {
				return ErrInvalidParams
			}
		}
	case Pending, Denied:
		if d.View != nil {
			return ErrInvalidParams
		}
	default:
		return ErrInvalidParams
	}
	return nil
}
func validateEntry(e *pb.ExportedEntry) error {
	if e == nil || e.Timestamp == nil || e.Timestamp.CheckValid() != nil {
		return ErrInvalidParams
	}
	if _, err := te.ParseSeq(e.Seq); err != nil {
		return ErrInvalidParams
	}
	if e.Payload != nil && (e.Payload.MediaType == "" || !json.Valid(e.Payload.JsonData)) {
		return ErrInvalidParams
	}
	return nil
}
func validatePage(p *pb.Page) error {
	if p == nil || p.Source == nil {
		return ErrInvalidParams
	}
	if _, err := te.ParseSeq(p.SnapshotHead); err != nil {
		return ErrInvalidParams
	}
	for _, e := range p.Entries {
		if err := validateEntry(e); err != nil {
			return err
		}
	}
	return nil
}
