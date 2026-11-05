package a2a

import (
	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
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
